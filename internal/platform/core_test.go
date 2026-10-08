package platform

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMessageValidationUnicodeAndIdentity(t *testing.T) {
	id := newID()
	if !validID(id) {
		t.Fatal("generated ID is invalid")
	}
	for _, tc := range []struct {
		name  string
		input sendMessageInput
		valid bool
	}{
		{"unicode boundary", sendMessageInput{ClientID: id, Body: strings.Repeat("中", 4000)}, true},
		{"too long", sendMessageInput{ClientID: id, Body: strings.Repeat("中", 4001)}, false},
		{"image only", sendMessageInput{ClientID: id, ImageID: newID()}, true},
		{"empty", sendMessageInput{ClientID: id, Body: " \n "}, false},
		{"forged client id", sendMessageInput{ClientID: "not-a-uuid", Body: "hello"}, false},
		{"invalid image id", sendMessageInput{ClientID: id, ImageID: "../other"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if (validateMessage(tc.input) == nil) != tc.valid {
				t.Fatalf("unexpected validation result: %+v", tc.input)
			}
		})
	}
}

func TestExplicitHandoffDoesNotOverrideNegation(t *testing.T) {
	for _, text := range []string{"请帮我转人工", "我要人工客服", "人工客服", "talk to a human"} {
		if !explicitHandoff(text) {
			t.Errorf("missed explicit request %q", text)
		}
	}
	for _, text := range []string{"不用转人工了", "不要转人工", "人工客服几点上线？", "我想问一下退货政策"} {
		if explicitHandoff(text) {
			t.Errorf("false handoff %q", text)
		}
	}
}

func TestConversationReadPermissions(t *testing.T) {
	s := NewServer(nil, nil, Config{})
	c := Conversation{VisitorID: "visitor-1", AgentID: "agent-1", Status: "waiting"}
	for _, tc := range []struct {
		actor   Actor
		allowed bool
	}{
		{Actor{ID: "visitor-1", Role: "visitor"}, true}, {Actor{ID: "visitor-2", Role: "visitor"}, false},
		{Actor{ID: "agent-1", Role: "agent"}, true}, {Actor{ID: "agent-2", Role: "agent"}, false},
		{Actor{ID: "admin", Role: "admin"}, true}, {Actor{ID: "visitor-1", Role: "unknown"}, false},
	} {
		if got := s.canRead(context.Background(), tc.actor, c); got != tc.allowed {
			t.Errorf("role %s id %s: got %v", tc.actor.Role, tc.actor.ID, got)
		}
	}
}

func TestCookieSurfacesAreIndependent(t *testing.T) {
	r := httptest.NewRequest("GET", "http://localhost/api/me", nil)
	if got := cookieName(r); got != "assist_staff" {
		t.Fatal(got)
	}
	r.Header.Set("X-Surface", "visitor")
	if got := cookieName(r); got != "assist_visitor" {
		t.Fatal(got)
	}
	r = httptest.NewRequest("GET", "http://localhost/api/attachments/x?surface=visitor", nil)
	if got := cookieName(r); got != "assist_visitor" {
		t.Fatal(got)
	}
}

func TestOriginAndCSRFMiddleware(t *testing.T) {
	s := NewServer(nil, nil, Config{AllowedOrigins: []string{"http://localhost:8090"}})
	for _, tc := range []struct {
		origin          string
		required, valid bool
	}{
		{"", false, true}, {"", true, false}, {"null", false, false},
		{"http://localhost:8090", true, true}, {"https://localhost:8090", true, false},
		{"http://localhost:8090.evil.test", true, false}, {"http://localhost:8090/path", true, false},
	} {
		r := httptest.NewRequest("GET", "http://localhost/api/ws", nil)
		r.Header.Set("Origin", tc.origin)
		if got := s.validOrigin(r, tc.required); got != tc.valid {
			t.Errorf("origin %q: %v", tc.origin, got)
		}
	}
	for _, tc := range []struct {
		origin, header string
		status         int
	}{
		{"", "", http.StatusForbidden}, {"http://evil.test", "support-agent", http.StatusForbidden},
		{"http://localhost:8090", "support-agent", http.StatusMethodNotAllowed}, {"", "support-agent", http.StatusMethodNotAllowed},
	} {
		r := httptest.NewRequest("POST", "http://localhost/api/nonexistent", strings.NewReader(`{}`))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("X-Requested-With", tc.header)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Errorf("origin %q header %q got %d", tc.origin, tc.header, w.Code)
		}
	}
}

func TestDecodeRejectsExtraPayloadAndSenderForgery(t *testing.T) {
	for _, body := range []string{`{"client_id":"x","body":"hello","sender_id":"admin"}`, `{"client_id":"x","body":"hello"} {}`, `{invalid}`} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		var input sendMessageInput
		if decodeJSON(httptest.NewRecorder(), r, &input) == nil {
			t.Errorf("accepted %s", body)
		}
	}
	var input sendMessageInput
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"client_id":"x","body":"hello"}`))
	if err := decodeJSON(httptest.NewRecorder(), r, &input); err != nil {
		t.Fatal(err)
	}
}

func TestUploadValidationAndFilename(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	mime, ext, err := validateImage(data)
	if err != nil || mime != "image/png" || ext != ".png" {
		t.Fatalf("valid png: %s %s %v", mime, ext, err)
	}
	if _, _, err = validateImage([]byte(`<svg onload="alert(1)"></svg>`)); err == nil {
		t.Fatal("accepted active content")
	}
	if _, _, err = validateImage(make([]byte, (5<<20)+1)); err == nil {
		t.Fatal("accepted oversized file")
	}
	if _, _, err = validateImage(data[:33]); err == nil {
		t.Fatal("accepted truncated image with valid header")
	}
	// Rewrite a valid PNG IHDR and CRC without allocating a huge decoded image.
	large := append([]byte(nil), data...)
	binary.BigEndian.PutUint32(large[16:20], 10000)
	binary.BigEndian.PutUint32(large[20:24], 10000)
	binary.BigEndian.PutUint32(large[29:33], crc32.ChecksumIEEE(large[12:29]))
	if _, _, err = validateImage(large); err == nil {
		t.Fatal("accepted pixel bomb")
	}
	if got := safeFilename(`C:\uploads\..\evil.png`); got != "evil.png" {
		t.Fatal(got)
	}
	if got := safeFilename("../../foo\n.png"); got != "foo.png" {
		t.Fatal(got)
	}
}

func TestPublicErrorsHideInternalDetails(t *testing.T) {
	if strings.Contains(publicError(errors.New("password=secret database failure")), "secret") {
		t.Fatal("leaked details")
	}
	if got := publicError(conflict("会话已结束")); got != "会话已结束" {
		t.Fatal(got)
	}
}

func TestStaffValidation(t *testing.T) {
	if err := validateStaffCredentials("客服", "agent@example.com", "sufficient-password"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range [][3]string{{"", "agent@example.com", "long-password"}, {"客服", "bad-email", "long-password"}, {"客服", "agent@example.com", "short"}} {
		if validateStaffCredentials(tc[0], tc[1], tc[2]) == nil {
			t.Fatal("accepted invalid credentials")
		}
	}
}

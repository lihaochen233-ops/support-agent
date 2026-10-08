package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAgentReplyRequiresRetrievedSources(t *testing.T) {
	source := Citation{ID: "retrieved", DocumentID: "doc", Text: "支持七日退货"}
	cases := []struct {
		name, body      string
		greeting, valid bool
	}{
		{"grounded", `{"kind":"answer","body":"七日内可以申请退货。","citation_ids":["retrieved"]}`, false, true},
		{"fabricated", `{"kind":"answer","body":"可以退货","citation_ids":["invented"]}`, false, false},
		{"unverified", `{"kind":"answer","body":"任何时候都能退货"}`, false, false},
		{"greeting", `{"kind":"answer","body":"您好，请问有什么可以帮助您？"}`, true, true},
		{"unknown permission", `{"kind":"refund","body":"已退款"}`, false, false},
		{"extra payload", `{"kind":"clarify","body":"订单是什么情况？"} {}`, false, false},
		{"extra field", `{"kind":"clarify","body":"什么情况？","execute":"refund"}`, false, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseAgentReply(test.body, map[string]Citation{source.ID: source}, test.greeting)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v error=%v", test.valid, err)
			}
		})
	}
}
func TestAgentCallsRetrievalThenCitesItsResult(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("invalid provider request")
		}
		if calls.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"call-1","type":"function","function":{"name":"search_knowledge","arguments":"{\"query\":\"七天退货\"}"}}]}}]}`))
			return
		}
		if !strings.Contains(string(request["messages"]), "policy-1") || !strings.Contains(string(request["messages"]), "call-1") {
			t.Error("retrieved source or tool correlation missing")
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"kind\":\"answer\",\"body\":\"满足条件可在七天内申请退货。\",\"citation_ids\":[\"policy-1\"]}"}}]}`))
	}))
	defer server.Close()
	searches := 0
	phases := []string{}
	reply, err := runAgent(context.Background(), providerClient{BaseURL: server.URL, APIKey: "fixture"}, []modelMessage{{Role: "user", Content: "能退货吗"}}, false, func(_ context.Context, q string) ([]Citation, error) {
		searches++
		if q != "七天退货" {
			t.Error("query differs")
		}
		return []Citation{{ID: "policy-1", DocumentID: "doc", Text: "七天退货规则"}}, nil
	}, func(phase string) { phases = append(phases, phase) })
	if err != nil || searches != 1 || calls.Load() != 2 || reply.Kind != "answer" || len(reply.Citations) != 1 {
		t.Fatalf("reply=%+v searches=%d calls=%d error=%v", reply, searches, calls.Load(), err)
	}
	if !strings.Contains(strings.Join(phases, ","), "正在检索知识库") {
		t.Error("missing retrieval status")
	}
}
func TestAgentRejectsUnknownToolAndBoundsCalls(t *testing.T) {
	for _, tool := range []string{"execute_refund", "search_knowledge"} {
		t.Run(tool, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"tool_calls": []any{map[string]any{"id": "tool", "type": "function", "function": map[string]string{"name": tool, "arguments": `{"query":"policy"}`}}}}}}})
			}))
			defer server.Close()
			_, err := runAgent(context.Background(), providerClient{BaseURL: server.URL, APIKey: "fixture"}, nil, false, func(context.Context, string) ([]Citation, error) { return []Citation{{ID: "source"}}, nil }, func(string) {})
			if err == nil || calls.Load() > 4 {
				t.Fatalf("tool %s was not bounded: calls=%d err=%v", tool, calls.Load(), err)
			}
		})
	}
}
func TestAgentNoEvidenceTransfersWithoutFurtherGeneration(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"search","type":"function","function":{"name":"search_knowledge","arguments":"{\"query\":\"unknown\"}"}}]}}]}`))
	}))
	defer server.Close()
	reply, err := runAgent(context.Background(), providerClient{BaseURL: server.URL, APIKey: "fixture"}, nil, false, func(context.Context, string) ([]Citation, error) { return nil, nil }, func(string) {})
	if err != nil || reply.Kind != "handoff" || calls.Load() != 1 {
		t.Fatalf("no evidence should hand off: %+v %v", reply, err)
	}
}
func TestParseDocumentUTF8AndInvalidInputs(t *testing.T) {
	dir := t.TempDir()
	for _, test := range []struct {
		name  string
		data  []byte
		valid bool
	}{
		{"policy.md", []byte("# 退货规则\n购买后七天内可申请退货。"), true},
		{"empty.txt", []byte("   \n"), false},
		{"invalid.txt", []byte{0xff, 0xfe, 0x81}, false},
		{"invalid.pdf", []byte("this is not PDF"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(dir, test.name)
			if err := os.WriteFile(path, test.data, 0600); err != nil {
				t.Fatal(err)
			}
			chunks, err := parseDocument(path, filepath.Ext(path))
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v chunks=%v error=%v", test.valid, chunks, err)
			}
		})
	}
}
func TestParseTextPDFPreservesPage(t *testing.T) {
	// A small, self-contained PDF fixture; no proprietary document or external CLI.
	objects := []string{
		`<< /Type /Catalog /Pages 2 0 R >>`,
		`<< /Type /Pages /Kids [3 0 R] /Count 1 >>`,
		`<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 300] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>`,
		`<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>`,
	}
	stream := "BT /F1 12 Tf 20 250 Td (Return policy: seven days.) Tj ET\n"
	objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream))
	var out strings.Builder
	out.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, object := range objects {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&out, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	path := filepath.Join(t.TempDir(), "fixture.pdf")
	if err := os.WriteFile(path, []byte(out.String()), 0600); err != nil {
		t.Fatal(err)
	}
	chunks, err := parseDocument(path, ".pdf")
	if err != nil || len(chunks) != 1 || chunks[0].Page != 1 || !strings.Contains(chunks[0].Text, "seven days") {
		t.Fatalf("PDF parse=%v err=%v", chunks, err)
	}
}

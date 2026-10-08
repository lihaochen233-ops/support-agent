FROM node:24-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.26-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /server ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && addgroup -S -g 10001 luma && adduser -S -D -u 10001 -G luma luma && mkdir /data && chown luma:luma /data
WORKDIR /app
COPY --from=backend /server /app/server
COPY --from=frontend /src/frontend/dist /app/frontend/dist
USER luma
EXPOSE 8090
ENTRYPOINT ["/app/server"]

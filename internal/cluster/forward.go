package cluster

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/textproto"
	"strconv"
)

// Wire format for a forwarded request/response pair.
//
//   request payload:   [u32 len of dumped HTTP request][dumped request bytes][optional remote-addr trailer]
//   response payload:  [byte version=1][u16 status][u32 header-bytes-len][headers][u32 body-len][body]
//
// Headers are wire-format: "Key: value\r\n" lines, terminated by "\r\n".
//
// We use httputil.DumpRequest for the request side because it produces a
// well-defined HTTP/1.1 wire representation that http.ReadRequest parses
// back natively, and it preserves all headers/cookies the leader needs to
// re-run auth and per-route logic.
//
// For the response side we craft a compact framing rather than reusing
// httputil.DumpResponse because the latter requires a *http.Response
// (which httptest.NewRecorder gives us) but the parser http.ReadResponse
// requires a corresponding Request — this version is simpler and cheaper.

const responseWireVersion byte = 1

// SerializeRequest writes r in HTTP/1.1 wire format and appends the original
// remote address as a trailer so the leader can preserve it for logging /
// rate-limiting decisions.
//
// httputil.DumpRequest doesn't auto-emit Content-Length from r.ContentLength,
// which makes http.ReadRequest on the leader read a zero-length body. We
// pre-buffer the body, swap it onto r, and set Content-Length explicitly
// before dumping so the round-trip preserves the payload.
func SerializeRequest(r *http.Request) ([]byte, error) {
	var bodyBytes []byte
	if r.Body != nil && r.Body != http.NoBody {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}
		bodyBytes = b
		// Restore the body so any downstream code that still inspects r
		// (e.g. logging middleware) doesn't see a drained reader.
		r.Body = io.NopCloser(bytes.NewReader(b))
	}
	r.ContentLength = int64(len(bodyBytes))
	r.Header.Set("Content-Length", strconv.Itoa(len(bodyBytes)))

	dumped, err := httputil.DumpRequest(r, true)
	if err != nil {
		return nil, fmt.Errorf("dump request: %w", err)
	}

	remote := []byte(r.RemoteAddr)

	buf := bytes.NewBuffer(make([]byte, 0, 4+len(dumped)+4+len(remote)))
	var u [4]byte
	binary.BigEndian.PutUint32(u[:], uint32(len(dumped)))
	buf.Write(u[:])
	buf.Write(dumped)
	binary.BigEndian.PutUint32(u[:], uint32(len(remote)))
	buf.Write(u[:])
	buf.Write(remote)
	return buf.Bytes(), nil
}

// DeserializeRequest reverses SerializeRequest.
func DeserializeRequest(data []byte) (*http.Request, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("forward: payload too small (%d bytes)", len(data))
	}
	reqLen := binary.BigEndian.Uint32(data[:4])
	if 4+int(reqLen) > len(data) {
		return nil, fmt.Errorf("forward: truncated request (need %d, have %d)", reqLen, len(data)-4)
	}
	reqBytes := data[4 : 4+reqLen]

	req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(reqBytes)))
	if err != nil {
		return nil, fmt.Errorf("read request: %w", err)
	}
	// http.ReadRequest leaves RequestURI populated and URL.Host/Scheme empty.
	// That's fine for serving via a Mux — Mux only inspects Method + URL.Path.

	// Optional remote-addr trailer.
	off := 4 + int(reqLen)
	if len(data) >= off+4 {
		raLen := binary.BigEndian.Uint32(data[off : off+4])
		off += 4
		if len(data) >= off+int(raLen) {
			req.RemoteAddr = string(data[off : off+int(raLen)])
		}
	}
	return req, nil
}

// runForwardedRequest is the leader-side glue that turns a serialized
// request into an actual HTTP call against the leader's mux and returns
// the serialized response. Used as bw cluster's WithForwardHandler.
func runForwardedRequest(ctx context.Context, h http.Handler, data []byte) []byte {
	req, err := DeserializeRequest(data)
	if err != nil {
		return encodeForwardError(http.StatusBadRequest, "leader: bad forwarded request: "+err.Error())
	}
	defer req.Body.Close()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req.WithContext(ctx))

	res := rec.Result()
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return encodeResponse(res.StatusCode, res.Header, body)
}

// encodeResponse serializes status + headers + body using the wire format
// described above. A trailing CRLF is appended to the headers so
// textproto.ReadMIMEHeader on the receiving end recognises the terminator.
func encodeResponse(status int, headers http.Header, body []byte) []byte {
	var hbuf bytes.Buffer
	_ = headers.Write(&hbuf) // never errors on a bytes.Buffer
	hbuf.WriteString("\r\n") // terminator required by textproto.ReadMIMEHeader

	buf := bytes.NewBuffer(make([]byte, 0, 1+2+4+hbuf.Len()+4+len(body)))
	buf.WriteByte(responseWireVersion)

	var u2 [2]byte
	binary.BigEndian.PutUint16(u2[:], uint16(status))
	buf.Write(u2[:])

	var u4 [4]byte
	binary.BigEndian.PutUint32(u4[:], uint32(hbuf.Len()))
	buf.Write(u4[:])
	buf.Write(hbuf.Bytes())

	binary.BigEndian.PutUint32(u4[:], uint32(len(body)))
	buf.Write(u4[:])
	buf.Write(body)
	return buf.Bytes()
}

// encodeForwardError builds a synthetic response that surfaces a leader-side
// failure to the original client.
func encodeForwardError(status int, msg string) []byte {
	hdr := http.Header{}
	hdr.Set("Content-Type", "text/plain; charset=utf-8")
	hdr.Set("X-Pika-Cluster-Error", "1")
	hdr.Set("Content-Length", strconv.Itoa(len(msg)))
	return encodeResponse(status, hdr, []byte(msg))
}

// WriteForwardedResponse decodes a wire response produced by encodeResponse
// and writes it to the original client's ResponseWriter.
func WriteForwardedResponse(w http.ResponseWriter, payload []byte) error {
	if len(payload) == 0 {
		http.Error(w, "leader returned empty response", http.StatusBadGateway)
		return nil
	}
	if payload[0] != responseWireVersion {
		http.Error(w, fmt.Sprintf("forward: unsupported response wire version %d", payload[0]), http.StatusBadGateway)
		return nil
	}
	if len(payload) < 1+2+4 {
		http.Error(w, "forward: truncated response header", http.StatusBadGateway)
		return nil
	}
	off := 1
	status := int(binary.BigEndian.Uint16(payload[off : off+2]))
	off += 2
	headerLen := int(binary.BigEndian.Uint32(payload[off : off+4]))
	off += 4
	if len(payload) < off+headerLen+4 {
		http.Error(w, "forward: truncated response headers", http.StatusBadGateway)
		return nil
	}
	headerBytes := payload[off : off+headerLen]
	off += headerLen
	bodyLen := int(binary.BigEndian.Uint32(payload[off : off+4]))
	off += 4
	if len(payload) < off+bodyLen {
		http.Error(w, "forward: truncated response body", http.StatusBadGateway)
		return nil
	}
	body := payload[off : off+bodyLen]

	// Parse headers (one "Key: Value\r\n" per line, terminated by blank line).
	hdr, err := parseResponseHeaders(headerBytes)
	if err != nil {
		http.Error(w, "forward: invalid response headers: "+err.Error(), http.StatusBadGateway)
		return nil
	}
	for k, vs := range hdr {
		// Skip hop-by-hop headers — they're only meaningful between the
		// original client and *us*, not between us and the leader.
		if isHopByHopHeader(k) {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(status)
	_, err = w.Write(body)
	return err
}

func parseResponseHeaders(b []byte) (http.Header, error) {
	// Re-use Go's MIME header parser. headers serialized by http.Header.Write
	// are CRLF-terminated and end with a blank line, which textproto requires.
	tp := textproto.NewReader(bufio.NewReader(bytes.NewReader(b)))
	mh, err := tp.ReadMIMEHeader()
	if err != nil && err != io.EOF {
		return nil, err
	}
	return http.Header(mh), nil
}

// isHopByHopHeader returns true for headers that must not be forwarded
// across an intermediary per RFC 7230 §6.1.
func isHopByHopHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Connection",
		"Keep-Alive",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Te",
		"Trailer",
		"Transfer-Encoding",
		"Upgrade":
		return true
	}
	return false
}

// Package httpapi serves the gateway commands over HTTP for clients without a
// live long link (the "short link").
//
// Request body: 1 xor byte k, then XOR(k, authToken(64) || xxtea(uuid(64) ||
// request protobuf, maskKey)). authToken and maskKey come from a prior AUTH.
// Response body: xxtea(response protobuf, maskKey).
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/xxtea/xxtea-go/xxtea"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"

	"github.com/yourname/roomkit/api/gen/clientpb"
	"github.com/yourname/roomkit/internal/gateway/handler"
	"github.com/yourname/roomkit/internal/gateway/store"
	"github.com/yourname/roomkit/pkg/log"
)

const (
	tokenLen = 64
	uuidLen  = 64
	// Overhead bounds the bytes a request body adds around its payload.
	Overhead = 1 + tokenLen + uuidLen + 8
)

// UserInfoLoader resolves a short-link auth token.
type UserInfoLoader interface {
	LoadUserInfo(ctx context.Context, authToken string) (*store.UserInfo, error)
}

// DecodeError is a malformed or unauthenticated short-link request.
type DecodeError struct{ Reason string }

func (e *DecodeError) Error() string { return "short link request decode error: " + e.Reason }

// Decode unwraps a short-link request body. The returned user may be non-nil
// together with an error so the failure can be answered with its key.
func Decode(ctx context.Context, users UserInfoLoader, body []byte) (*store.UserInfo, []byte, error) {
	if len(body) < 2 {
		return nil, nil, &DecodeError{"xor target not found"}
	}
	key := body[0]
	data := make([]byte, len(body)-1)
	for i, b := range body[1:] {
		data[i] = b ^ key
	}
	if len(data) <= tokenLen {
		return nil, nil, &DecodeError{"auth token not found"}
	}
	user, err := users.LoadUserInfo(ctx, string(data[:tokenLen]))
	if err != nil {
		if errors.Is(err, store.ErrUnknownToken) {
			return nil, nil, &DecodeError{"auth info not exist"}
		}
		return nil, nil, fmt.Errorf("load auth info: %w", err)
	}
	plain := xxtea.Decrypt(data[tokenLen:], []byte(user.MaskKey))
	if plain == nil {
		return user, nil, &DecodeError{"xxtea decode error"}
	}
	if len(plain) < uuidLen {
		return user, nil, &DecodeError{"request uuid not found"}
	}
	return user, plain[uuidLen:], nil
}

// Encode builds a short-link request body; it is the client-side
// counterpart of Decode.
func Encode(xorKey byte, authToken, maskKey string, uuid [uuidLen]byte, payload []byte) []byte {
	inner := append(uuid[:], payload...)
	data := append([]byte(authToken), xxtea.Encrypt(inner, []byte(maskKey))...)
	out := make([]byte, 1, 1+len(data))
	out[0] = xorKey
	for _, b := range data {
		out = append(out, b^xorKey)
	}
	return out
}

// Server is the short-link HTTP server.
type Server struct {
	svc     *handler.Service
	users   UserInfoLoader
	maxBody int64
	ln      net.Listener
	srv     *http.Server
}

// Listen binds addr. maxBody bounds the request body size.
func Listen(ctx context.Context, addr string, svc *handler.Service, users UserInfoLoader, maxBody int) (*Server, error) {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	s := &Server{svc: svc, users: users, maxBody: int64(maxBody), ln: ln}
	s.srv = &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	return s, nil
}

// Addr is the bound address.
func (s *Server) Addr() net.Addr { return s.ln.Addr() }

// Serve blocks until Shutdown.
func (s *Server) Serve() error {
	if err := s.srv.Serve(s.ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown stops accepting requests and waits for in-flight ones.
func (s *Server) Shutdown(ctx context.Context) error { return s.srv.Shutdown(ctx) }

// Handler registers every command URL plus /healthz.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, rt := range s.svc.Routes() {
		mux.Handle("POST "+rt.URL, s.command(rt))
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	return mux
}

func (s *Server) command(rt *handler.Route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.maxBody))
		if err != nil {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}

		user, payload, err := Decode(r.Context(), s.users, body)
		var resp proto.Message
		if err != nil {
			var de *DecodeError
			msg := "short link request decode error"
			if errors.As(err, &de) {
				msg = de.Error()
			}
			log.L().Info("short link decode", zap.String("url", rt.URL), zap.String("remote", r.RemoteAddr), zap.Error(err))
			resp = rt.Fail(clientpb.ErrorCode_SHORT_LINK_REQUEST_DECODE_ERROR, msg)
		} else {
			req := s.svc.NewShortLinkRequest(rt.Cmd, user, remoteIP(r))
			resp = rt.Serve(r.Context(), req, payload)
		}

		raw, err := proto.Marshal(resp)
		if err != nil {
			log.L().Error("marshal short link response", zap.String("url", rt.URL), zap.Error(err))
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		maskKey := ""
		if user != nil {
			maskKey = user.MaskKey
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		if _, err := w.Write(xxtea.Encrypt(raw, []byte(maskKey))); err != nil {
			log.L().Debug("write short link response", zap.Error(err))
		}
	})
}

func remoteIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

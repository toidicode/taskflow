package notifyrpc

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"log/slog"
	"runtime/debug"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc/filters"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/toidicode/taskflow/internal/reqlog"
)

// Key metadata (như header HTTP, viết thường) mà client TaskFlow gửi kèm mỗi RPC.
const (
	mdAuthorization = "authorization" // "Bearer <TASKFLOW_NOTIFIER_TOKEN>"
	mdRequestID     = "x-request-id"
)

// healthPrefix: RPC health check được gọi không cần token (Docker, Kubernetes không gửi token).
const healthPrefix = "/grpc.health.v1.Health/"

// ServerOptions trả về option cho grpc.NewServer: tracing, interceptor log, recover, auth (theo thứ tự
// từ ngoài vào trong) và luật keepalive.
func ServerOptions(logger *slog.Logger, token string) []grpc.ServerOption {
	a := auth{token: []byte(token)}
	return []grpc.ServerOption{
		// span server cho mỗi RPC, nối vào trace của client; health check không cần trace
		grpc.StatsHandler(otelgrpc.NewServerHandler(otelgrpc.WithFilter(filters.Not(filters.HealthCheck())))),
		grpc.ChainUnaryInterceptor(logUnary(logger), recoverUnary(logger), a.unary),
		grpc.ChainStreamInterceptor(logStream(logger), recoverStream(logger), a.stream),
		// client ping nhanh hơn MinTime thì server đóng kết nối (GOAWAY "too_many_pings");
		// mặc định MinTime là 5 phút, client của TaskFlow ping mỗi 30 giây
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 20 * time.Second, PermitWithoutStream: true}),
	}
}

// logUnary gắn request ID (từ metadata, không có thì tự sinh) vào ctx rồi ghi một dòng log cho mỗi RPC.
func logUnary(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx = withRequestID(ctx) // metadata x-request-id, không có thì rand.Text()
		start := time.Now()
		resp, err := handler(ctx, req)
		logRPC(ctx, logger, info.FullMethod, start, err) // method, code, duration, timeout
		return resp, err
	}
}

// logStream như logUnary cho RPC streaming. ServerStream không có cách đổi ctx nên phải bọc lại.
func logStream(logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := withRequestID(ss.Context())
		start := time.Now()
		err := handler(srv, serverStream{ss, ctx})
		logRPC(ctx, logger, info.FullMethod, start, err)
		return err
	}
}

// serverStream là ServerStream với ctx khác: handler gọi stream.Context() nhận ctx có request ID.
type serverStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s serverStream) Context() context.Context { return s.ctx }

func withRequestID(ctx context.Context) context.Context {
	var id string
	if v := metadata.ValueFromIncomingContext(ctx, mdRequestID); len(v) > 0 {
		id = v[0]
	}
	if !reqlog.Valid(id) {
		id = rand.Text()
	}
	return reqlog.WithID(ctx, id)
}

// logRPC ghi method, status code, thời gian xử lý và thời gian client cho phép (deadline).
func logRPC(ctx context.Context, logger *slog.Logger, method string, start time.Time, err error) {
	code := status.Code(err)
	if code == codes.OK && strings.HasPrefix(method, healthPrefix) {
		return // Docker hỏi health mỗi 10 giây: không ghi log khi ổn
	}
	attrs := []any{"method", method, "code", code.String(), "duration", time.Since(start).Round(time.Microsecond)}
	if d, ok := ctx.Deadline(); ok {
		attrs = append(attrs, "timeout", d.Sub(start).Round(time.Millisecond))
	}
	level := slog.LevelInfo
	switch code {
	case codes.OK:
	case codes.Internal, codes.Unknown, codes.DataLoss:
		level = slog.LevelError
		attrs = append(attrs, "err", status.Convert(err).Message())
	default:
		level = slog.LevelWarn
		attrs = append(attrs, "err", status.Convert(err).Message())
	}
	logger.Log(ctx, level, "rpc", attrs...)
}

// recoverUnary đổi panic trong handler thành lỗi Internal. Không có nó, một panic làm sập cả
// process notifier: gRPC không tự recover như net/http.
func recoverUnary(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if p := recover(); p != nil {
				err = panicError(ctx, logger, info.FullMethod, p)
			}
		}()
		return handler(ctx, req)
	}
}

func recoverStream(logger *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer func() {
			if p := recover(); p != nil {
				err = panicError(ss.Context(), logger, info.FullMethod, p)
			}
		}()
		return handler(srv, ss)
	}
}

func panicError(ctx context.Context, logger *slog.Logger, method string, p any) error {
	logger.ErrorContext(ctx, "panic trong RPC", "method", method, "panic", p, "stack", string(debug.Stack()))
	return status.Error(codes.Internal, "lỗi nội bộ") // không gửi chi tiết panic cho client
}

// auth kiểm tra token chung (shared secret) trong metadata "authorization".
type auth struct {
	token []byte
}

func (a auth) check(ctx context.Context, method string) error {
	if strings.HasPrefix(method, healthPrefix) { // "/grpc.health.v1.Health/": Docker không gửi token
		return nil
	}
	var got string
	if v := metadata.ValueFromIncomingContext(ctx, mdAuthorization); len(v) > 0 {
		got, _ = strings.CutPrefix(v[0], "Bearer ")
	}
	// so sánh thời gian hằng: thời gian trả lời không cho biết token đúng tới ký tự thứ mấy
	if got == "" || subtle.ConstantTimeCompare([]byte(got), a.token) != 1 {
		return status.Error(codes.Unauthenticated, "thiếu hoặc sai token")
	}
	return nil
}

func (a auth) unary(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if err := a.check(ctx, info.FullMethod); err != nil {
		return nil, err
	}
	return handler(ctx, req)
}

func (a auth) stream(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	if err := a.check(ss.Context(), info.FullMethod); err != nil {
		return err
	}
	return handler(srv, ss)
}

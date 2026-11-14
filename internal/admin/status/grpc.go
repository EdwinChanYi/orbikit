package status

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// GRPCHealth checks the standard gRPC health service at a host:port.
type GRPCHealth struct{}

// Check dials addr and reports serving when the empty service name is SERVING.
func (GRPCHealth) Check(ctx context.Context, addr string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return "", err
	}
	defer conn.Close()
	resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		return "", err
	}
	if resp.GetStatus() == healthpb.HealthCheckResponse_SERVING {
		return "serving", nil
	}
	return "not_serving", nil
}

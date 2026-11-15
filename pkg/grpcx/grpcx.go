// Package grpcx holds shared gRPC client and server construction.
package grpcx

import (
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"

	"github.com/EdwinChanYi/orbikit/pkg/discovery"
)

// Dial creates a lazily-connecting client. Use a "dns:///host:port" target to
// load-balance across every address the name resolves to.
func Dial(target string) (*grpc.ClientConn, error) {
	return dial(target, nil)
}

// DialDiscovered dials target. An "etcd:///service" target is resolved through
// reg; reg may be nil for direct addresses.
func DialDiscovered(target string, reg *discovery.Registry) (*grpc.ClientConn, error) {
	var extra []grpc.DialOption
	if discovery.UsesEtcd(target) {
		if reg == nil || reg.Builder() == nil {
			return nil, fmt.Errorf("grpc target %q requires etcd.endpoints", target)
		}
		extra = append(extra, grpc.WithResolvers(reg.Builder()))
	}
	return dial(target, extra)
}

func dial(target string, extra []grpc.DialOption) (*grpc.ClientConn, error) {
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(`{"loadBalancingConfig":[{"round_robin":{}}]}`),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                30 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	}
	opts = append(opts, extra...)
	return grpc.NewClient(target, opts...)
}

// NewServer creates a server with the standard health service registered.
func NewServer(opts ...grpc.ServerOption) *grpc.Server {
	opts = append([]grpc.ServerOption{
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	}, opts...)
	s := grpc.NewServer(opts...)
	healthpb.RegisterHealthServer(s, health.NewServer())
	return s
}

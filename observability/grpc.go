package telemetry

import (
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
)

// GRPCServerOptions returns the gRPC server options that trace every server
// RPC (span kind = server, attributes from the OTel gRPC semconv) and record
// grpc.server.* metrics.
//
//	grpc.NewServer(append(telemetry.GRPCServerOptions(), myOptions...)...)
func GRPCServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
	}
}

// GRPCClientDialOptions returns the dial options that trace every outgoing
// RPC and inject the W3C trace context into the gRPC metadata, so the callee
// joins the caller's trace.
//
//	grpc.NewClient(addr, append(telemetry.GRPCClientDialOptions(), grpc.WithTransportCredentials(...))...)
func GRPCClientDialOptions() []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	}
}

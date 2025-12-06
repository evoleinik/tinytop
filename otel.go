package main

import (
	"context"
	"net"
	"sync"

	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/grpc"
)

// TokenSample holds token usage from Claude Code
type TokenSample struct {
	Input      uint64
	Output     uint64
	CacheRead  uint64
	CacheWrite uint64
}

// OTELReceiver receives OpenTelemetry metrics via gRPC
type OTELReceiver struct {
	collectormetrics.UnimplementedMetricsServiceServer
	mu      sync.RWMutex
	current TokenSample
	server  *grpc.Server
	addr    string
}

// NewOTELReceiver creates a new gRPC receiver
func NewOTELReceiver(addr string) *OTELReceiver {
	r := &OTELReceiver{
		addr:   addr,
		server: grpc.NewServer(),
	}
	collectormetrics.RegisterMetricsServiceServer(r.server, r)
	return r
}

// Start begins listening for metrics
func (r *OTELReceiver) Start() error {
	lis, err := net.Listen("tcp", r.addr)
	if err != nil {
		return err
	}
	return r.server.Serve(lis)
}

// GetSample returns the current token sample
func (r *OTELReceiver) GetSample() TokenSample {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.current
}

// Export implements the OTLP MetricsService
func (r *OTELReceiver) Export(ctx context.Context, req *collectormetrics.ExportMetricsServiceRequest) (*collectormetrics.ExportMetricsServiceResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, rm := range req.GetResourceMetrics() {
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				if m.GetName() != "claude_code.token.usage" {
					continue
				}

				// Handle Sum metric type
				if sum := m.GetSum(); sum != nil {
					for _, dp := range sum.GetDataPoints() {
						tokenType := ""
						for _, attr := range dp.GetAttributes() {
							if attr.GetKey() == "type" {
								tokenType = attr.GetValue().GetStringValue()
							}
						}
						value := uint64(dp.GetAsDouble())
						switch tokenType {
						case "input":
							r.current.Input = value
						case "output":
							r.current.Output = value
						case "cacheRead":
							r.current.CacheRead = value
						case "cacheCreation":
							r.current.CacheWrite = value
						}
					}
				}
			}
		}
	}

	return &collectormetrics.ExportMetricsServiceResponse{}, nil
}

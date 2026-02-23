// Package consolidator provides memory consolidation services that analyze
// conversations and extract long-term memories via LLM-based compaction.
package consolidator

import (
	"context"
	"fmt"
	"io"

	pb "github.com/haryoiro/suzuha/gen/consolidator/v1"
	"github.com/haryoiro/suzuha/internal/memory"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// GRPCClient implements Client via gRPC.
type GRPCClient struct {
	conn   *grpc.ClientConn
	client pb.ConsolidatorServiceClient
}

// NewGRPCClient dials the consolidator gRPC server and returns a Client.
// The caller should close the returned io.Closer when done.
func NewGRPCClient(address string) (*GRPCClient, error) {
	conn, err := grpc.NewClient(address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("consolidator: dial %s: %w", address, err)
	}
	return &GRPCClient{
		conn:   conn,
		client: pb.NewConsolidatorServiceClient(conn),
	}, nil
}

// Compact sends a compaction request to the consolidator.
func (c *GRPCClient) Compact(ctx context.Context, req *CompactRequest) (*CompactResult, error) {
	// Convert internal → proto.
	pbReq := &pb.CompactRequest{
		TargetCount: int32(req.TargetCount),
		Messages:    make([]*pb.ChatMessage, len(req.Messages)),
	}
	for i, m := range req.Messages {
		pbReq.Messages[i] = &pb.ChatMessage{
			Role:          m.Role,
			Content:       m.Content,
			UserId:        m.UserID,
			UserName:      m.UserName,
			Source:        m.Source,
			Channel:       m.Channel,
			MessageId:     m.MessageID,
			TimestampUnix: m.Timestamp.Unix(),
		}
	}

	// RPC call.
	resp, err := c.client.Compact(ctx, pbReq)
	if err != nil {
		return nil, fmt.Errorf("consolidator: compact rpc: %w", err)
	}

	// Convert proto → internal.
	return protoToResult(resp), nil
}

// Close closes the gRPC connection.
func (c *GRPCClient) Close() error {
	return c.conn.Close()
}

// Ensure GRPCClient implements Client and io.Closer at compile time.
var (
	_ Client    = (*GRPCClient)(nil)
	_ io.Closer = (*GRPCClient)(nil)
)

// protoToResult converts a proto CompactResponse to an internal CompactResult.
func protoToResult(resp *pb.CompactResponse) *CompactResult {
	result := &CompactResult{}

	// Keep indices.
	result.KeepIndices = make([]int, len(resp.KeepIndices))
	for i, idx := range resp.KeepIndices {
		result.KeepIndices[i] = int(idx)
	}

	// Memories.
	result.Memories = make([]memory.Memory, len(resp.Memories))
	for i, m := range resp.Memories {
		result.Memories[i] = memory.Memory{
			Type:    memory.MemoryType(m.Type),
			Content: m.Content,
		}
	}

	// Affinity deltas.
	result.AffinityDeltas = make([]AffinityDelta, len(resp.AffinityDeltas))
	for i, d := range resp.AffinityDeltas {
		indices := make([]int, len(d.MessageIndices))
		for j, idx := range d.MessageIndices {
			indices[j] = int(idx)
		}
		result.AffinityDeltas[i] = AffinityDelta{
			PlatformUserID: d.PlatformUserId,
			Platform:       d.Platform,
			Delta:          d.Delta,
			Reason:         d.Reason,
			MessageIndices: indices,
		}
	}

	return result
}

package consolidator

import (
	"context"
	"time"

	pb "github.com/haryoiro/suzuha/gen/consolidator/v1"
	"github.com/haryoiro/suzuha/internal/llm"
	"google.golang.org/grpc"
)

// GRPCServer wraps Server to implement the generated ConsolidatorServiceServer interface.
type GRPCServer struct {
	pb.UnimplementedConsolidatorServiceServer
	srv *Server
}

// NewGRPCServer creates a GRPCServer that delegates to the given Server.
func NewGRPCServer(srv *Server) *GRPCServer {
	return &GRPCServer{srv: srv}
}

// Register registers the service on a gRPC server.
func (g *GRPCServer) Register(s *grpc.Server) {
	pb.RegisterConsolidatorServiceServer(s, g)
}

// Compact implements ConsolidatorServiceServer.
func (g *GRPCServer) Compact(ctx context.Context, req *pb.CompactRequest) (*pb.CompactResponse, error) {
	// Convert proto → internal types.
	messages := protoToMessages(req.Messages)
	internal := &CompactRequest{
		Messages:    messages,
		TargetCount: int(req.TargetCount),
	}

	// Delegate to business logic.
	result, err := g.srv.Compact(ctx, internal)
	if err != nil {
		return nil, err
	}

	// Convert internal → proto.
	return resultToProto(result), nil
}

// protoToMessages converts proto ChatMessages to internal llm.Messages.
func protoToMessages(msgs []*pb.ChatMessage) []llm.Message {
	out := make([]llm.Message, len(msgs))
	for i, m := range msgs {
		out[i] = llm.Message{
			Role:      m.Role,
			Content:   m.Content,
			UserID:    m.UserId,
			UserName:  m.UserName,
			Source:    m.Source,
			Channel:   m.Channel,
			MessageID: m.MessageId,
			Timestamp: time.Unix(m.TimestampUnix, 0),
		}
	}
	return out
}

// resultToProto converts an internal CompactResult to a proto CompactResponse.
func resultToProto(r *CompactResult) *pb.CompactResponse {
	resp := &pb.CompactResponse{}

	// Keep indices.
	resp.KeepIndices = make([]int32, len(r.KeepIndices))
	for i, idx := range r.KeepIndices {
		resp.KeepIndices[i] = int32(idx)
	}

	// Memories.
	resp.Memories = make([]*pb.ExtractedMemory, len(r.Memories))
	for i, m := range r.Memories {
		resp.Memories[i] = &pb.ExtractedMemory{
			Type:    string(m.Type),
			Content: m.Content,
		}
	}

	// Affinity deltas.
	resp.AffinityDeltas = make([]*pb.AffinityDelta, len(r.AffinityDeltas))
	for i, d := range r.AffinityDeltas {
		indices := make([]int32, len(d.MessageIndices))
		for j, idx := range d.MessageIndices {
			indices[j] = int32(idx)
		}
		resp.AffinityDeltas[i] = &pb.AffinityDelta{
			PlatformUserId: d.PlatformUserID,
			Platform:       d.Platform,
			Delta:          d.Delta,
			Reason:         d.Reason,
			MessageIndices: indices,
		}
	}

	return resp
}

import "@typespec/http";
import "@typespec/rest";

using TypeSpec.Http;
using TypeSpec.Rest;

namespace SuzuhaAdmin;

model Memory {
  id: string;
  type: string;
  content: string;
  metadata?: Record<unknown>;
  created_at: string;
  updated_at: string;
}

model MemoryWithVec extends Memory {
  has_embedding: boolean;
}

model VecStats {
  total_memories: int32;
  embedded_count: int32;
  missing_count: int32;
  coverage_pct: float64;
}

model DuplicateGroup {
  memories: Memory[];
}

model CreateMemoryRequest {
  type: string;
  content: string;
  metadata?: Record<unknown>;
}

model UpdateMemoryRequest {
  type?: string;
  content?: string;
  metadata?: Record<unknown>;
}

@route("/api/memories")
@tag("Memories")
interface Memories {
  @get op list(
    @query offset?: int32,
    @query limit?: int32,
    @query type?: string,
    @query q?: string,
    @query order?: string,
    @query dir?: string,
  ): PaginatedList<Memory>;

  @post op create(@body body: CreateMemoryRequest): {
    @statusCode statusCode: 201;
    @body body: { data: Memory };
  };

  @route("{id}") @get op get(@path id: string): { data: Memory };
  @route("{id}") @put op update(@path id: string, @body body: UpdateMemoryRequest): { data: Memory };
  @route("{id}") @delete op delete(@path id: string): { @statusCode statusCode: 204 };

  @route("vec-stats") @get op vecStats(): VecStats;
  @route("with-vec") @get op listWithVec(
    @query offset?: int32,
    @query limit?: int32,
    @query type?: string,
    @query q?: string,
    @query order?: string,
    @query dir?: string,
  ): PaginatedList<MemoryWithVec>;

  @route("duplicates") @get op duplicates(@query threshold?: float64): PaginatedList<DuplicateGroup>;
}

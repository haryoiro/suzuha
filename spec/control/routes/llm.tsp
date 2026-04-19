import "@typespec/http";
import "@typespec/rest";
import "@typespec/openapi";

using TypeSpec.Http;
using TypeSpec.Rest;
using TypeSpec.OpenAPI;

namespace SuzuhaControl;

model LLMStatus {
  provider: string;
  model_id: string;
  api_base: string;
  max_ctx: int32;
  vision: boolean;
  assignments: Record<unknown>[];
}

/**
 * LLM プロバイダ定義。ListProviders/SaveModel のレスポンス・リクエストに使う。
 * 構造は llm.Provider と一致させるため Record<unknown> で受ける。
 */
@tag("LLM")
@route("/internal/llm")
interface LLM {
  /**
   * 現在有効な conversation ロールのプロバイダ情報と全ロール割当を返す。
   */
  @get
  @extension("x-ogen-operation-group", "LLM")
  op status(): LLMStatus;

  /**
   * 登録済み LLM プロバイダ一覧を返す。
   */
  @route("providers")
  @get
  @extension("x-ogen-operation-group", "LLM")
  op listProviders(): Record<unknown>[];

  /**
   * 登録済みモデル一覧を返す。provider クエリで絞り込み可能。
   */
  @route("models")
  @get
  @extension("x-ogen-operation-group", "LLM")
  op listModels(@query provider?: string): Record<unknown>[];

  /**
   * モデルを 1 件登録/更新する。provider_name と model_id が必須。
   */
  @route("models")
  @post
  @extension("x-ogen-operation-group", "LLM")
  op saveModel(@body req: SaveModelRequest): OkResponse;

  /**
   * 登録プロバイダの API からモデルカタログを再取得して upsert する。
   */
  @route("models/refresh")
  @post
  @extension("x-ogen-operation-group", "LLM")
  op refreshModels(): ModelsRefreshResponse;

  /**
   * ロール割当一覧を返す。
   */
  @route("roles")
  @get
  @extension("x-ogen-operation-group", "LLM")
  op listRoles(): Record<unknown>[];

  /**
   * 指定ロールにプロバイダ+モデルを割り当てる。
   * conversation ロール変更時は token counter と max tokens も自動追従する。
   */
  @route("roles/{role}")
  @put
  @extension("x-ogen-operation-group", "LLM")
  op assignRole(@path role: string, @body req: AssignRoleRequest): OkResponse;
}

model ModelsRefreshResponse {
  ok: boolean;
  models_updated: int32;
}

model AssignRoleRequest {
  provider: string;
  model_id: string;
}

/**
 * llm.ModelInfo と対応するフィールド。
 */
model SaveModelRequest {
  provider_name: string;
  model_id: string;
  capabilities?: string[];
  max_context?: int32;
  source?: string;
}

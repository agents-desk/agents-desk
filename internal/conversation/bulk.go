package conversation

import (
	"context"

	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/i18n"
)

// BulkIDs 是批量操作的会话，最多 100 个。
type BulkIDs struct {
	IDs []string `json:"ids" minItems:"1" maxItems:"100" uniqueItems:"true" doc:"会话的 ID"`
}

// BulkResult 是批量操作中一个会话的结果：成功，或者失败的错误码和提示。
type BulkResult struct {
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty" doc:"失败时的错误码，例如 conversation.not_found"`
	Message string `json:"message,omitempty" doc:"失败时给用户看的提示"`
}

// BulkOutput 是批量操作的响应：每个会话一项，顺序与请求相同。
type BulkOutput struct {
	Body []BulkResult
}

// bulk 对 ids 中的每个会话依次执行 do，每个会话一个事务，一个失败不影响其他的。业务错误记在这个会话的结果中，
// 其他错误（例如数据库不可用）直接返回。
func bulk(ctx context.Context, ids []string, do func(id string) error) ([]BulkResult, error) {
	out := make([]BulkResult, len(ids))
	for i, id := range ids {
		out[i] = BulkResult{ID: id, OK: true}
		err := do(id)
		if err == nil {
			continue
		}
		code := errorx.CodeOf(err)
		if code == "" || errorx.KindOf(err).Systemic() {
			return nil, err
		}
		out[i] = BulkResult{ID: id, Code: code, Message: i18n.T(ctx, code, errorx.PublicOf(err))}
	}
	return out, nil
}

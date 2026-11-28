package agent

import "context"

// KnowledgeVisibility 控制知识库切片是否允许暴露给外部客户或仅限内部坐席与草稿使用。
type KnowledgeVisibility string

const (
	// KnowledgeExternal 表示已发布且允许对外客户引用的知识切片。
	KnowledgeExternal KnowledgeVisibility = "external"
	// KnowledgeInternal 表示仅限内部坐席、内部单聊与 Copilot 草稿参考的内部知识切片。
	KnowledgeInternal KnowledgeVisibility = "internal"
)

// RetrieveRequest 描述一次严格租户隔离的知识检索请求。
type RetrieveRequest struct {
	TenantID   string
	Query      string
	Language   string
	Visibility KnowledgeVisibility
	ChannelID  string
	Limit      int
}

// Citation 是命中的一条知识库切片证据。
type Citation struct {
	DocumentID string              `json:"document_id"`
	ChunkID    string              `json:"chunk_id"`
	Title      string              `json:"title"`
	Snippet    string              `json:"snippet"`
	Score      float64             `json:"score"`
	Visibility KnowledgeVisibility `json:"visibility"`
}

// Retriever 是工作区知识库检索契约；后续 RAG 模块直接实现该接口注入 Service 即可复用权限与引用归因。
type Retriever interface {
	Retrieve(ctx context.Context, req RetrieveRequest) ([]Citation, error)
}

// EmptyRetriever 是未配置知识库时的默认实现，返回空结果。
type EmptyRetriever struct{}

// Retrieve 返回空的检索结果。
func (EmptyRetriever) Retrieve(context.Context, RetrieveRequest) ([]Citation, error) {
	return nil, nil
}

package widget

import (
	"context"
	"fmt"
	"strings"

	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/crypt"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/channel"
)

// InstallationDTO 是把聊天插件装到产品网站上要用的内容。
type InstallationDTO struct {
	EmbedCode      string `json:"embed_code" doc:"放进网站每个页面的 </body> 之前的代码"`
	IdentitySecret string `json:"identity_secret" doc:"身份验证密钥：产品后端用它签名已登录用户的身份（HS256 的 JWT），只保存在产品后端"`
}

// InstallationOutput 是返回安装内容的响应。
type InstallationOutput struct {
	Body InstallationDTO
}

// GetInstallation 读取聊天插件的嵌入代码和身份验证密钥。
type GetInstallation struct {
	db        *bun.DB
	crypt     *crypt.Crypt
	publicURL string
}

// NewGetInstallation 返回读取安装内容的动作；crypt 解开身份验证密钥，publicURL 是应用对外的地址，嵌入代码从这里加载聊天窗口。
func NewGetInstallation(db *bun.DB, c *crypt.Crypt, publicURL string) *GetInstallation {
	return &GetInstallation{db: db, crypt: c, publicURL: publicURL}
}

// Handle 返回 sub 所在工作区中聊天插件 id 的嵌入代码和身份验证密钥，不存在时返回 channel.ErrNotFound。
func (a *GetInstallation) Handle(ctx context.Context, sub identity.Subject, id string) (*InstallationDTO, error) {
	ch, err := find(ctx, a.db, sub.Tenant, id, false)
	if err != nil {
		return nil, err
	}
	return installation(ch, a.crypt, a.publicURL)
}

// HTTP 处理 GET /t/{slug}/api/widgets/{id}/installation。
func (a *GetInstallation) HTTP(ctx context.Context, in *IDInput) (*InstallationOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.ID)
	if err != nil {
		return nil, err
	}
	return &InstallationOutput{Body: *dto}, nil
}

// installation 返回聊天插件 ch 的安装内容。嵌入代码在页面上声明渠道，再加载 publicURL 下的 /widget/loader.js，
// 由它打开聊天窗口；已登录的用户在 user_token 中带上产品后端签名的身份。
func installation(ch *channel.Channel, c *crypt.Crypt, publicURL string) (*InstallationDTO, error) {
	s, err := settingsOf(ch)
	if err != nil {
		return nil, err
	}
	secret, err := c.Decrypt(s.IdentitySecret)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(publicURL, "/")
	code := fmt.Sprintf(`<script>
  window.agentsDesk = { channel: %q };
  // 已登录的用户：window.agentsDesk.user_token = "<产品后端用身份验证密钥签名的 JWT>";
</script>
<script async src="%s/widget/loader.js"></script>`, ch.ID, base)
	return &InstallationDTO{EmbedCode: code, IdentitySecret: secret}, nil
}

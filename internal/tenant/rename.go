package tenant

import (
	"context"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/feature"
	"github.com/go-tenon/tenon/filesystem"
	"github.com/go-tenon/tenon/identity"

	"agents-desk/internal/file"
)

// GeneralSettings 是工作区的常规设置：名称、Logo 和单窗口渠道的重开窗口。
type GeneralSettings struct {
	Name              string  `json:"name" minLength:"1" maxLength:"60"`
	LogoFileID        *string `json:"logo_file_id,omitempty" maxLength:"36" doc:"工作区 Logo 文件 ID：传空字符串表示移除，不传保持原样"`
	ReopenWindowHours *int    `json:"reopen_window_hours,omitempty" minimum:"0" maximum:"720" doc:"会话关闭后的重开窗口（小时），不传保持原样，0 表示关闭后直接新建会话"`
}

// RenameInput 是修改常规设置的 HTTP 请求。
type RenameInput struct {
	Body GeneralSettings
}

// Rename 修改当前工作区的常规设置：名称、Logo 和重开窗口。地址（slug）创建后不能修改。
type Rename struct {
	db       *bun.DB
	files    *filesystem.Service
	features []*feature.Store
}

// NewRename 返回修改工作区常规设置的动作。
func NewRename(db *bun.DB, files *filesystem.Service, features ...*feature.Store) *Rename {
	return &Rename{db: db, files: files, features: features}
}

// Handle 更新 sub 所在工作区的常规设置并记录操作日志，返回修改后的当前工作区。
func (a *Rename) Handle(ctx context.Context, sub identity.Subject, s GeneralSettings) (*CurrentDTO, error) {
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		t, err := find(ctx, tx, sub.Tenant, true)
		if err != nil {
			return err
		}
		nameChanged := t.Name != s.Name
		reopenChanged := s.ReopenWindowHours != nil && t.ReopenWindowHours != *s.ReopenWindowHours
		var logoChanged bool
		var nextLogo *uuid.UUID
		if s.LogoFileID != nil {
			nextLogo, err = file.ReplaceImage(ctx, tx, a.files, sub, "logo_file_id", file.Logo, t.LogoFileID, strings.TrimSpace(*s.LogoFileID))
			if err != nil {
				return err
			}
			logoChanged = !sameUUID(t.LogoFileID, nextLogo)
		}
		if !nameChanged && !reopenChanged && !logoChanged {
			return nil
		}
		fromName := t.Name
		fromReopen := t.ReopenWindowHours
		t.Name, t.UpdatedAt = s.Name, time.Now().UTC().Truncate(time.Microsecond)
		cols := []string{"updated_at"}
		if nameChanged {
			cols = append(cols, "name")
		}
		if logoChanged {
			t.LogoFileID = nextLogo
			cols = append(cols, "logo_file_id")
		}
		if reopenChanged {
			t.ReopenWindowHours = *s.ReopenWindowHours
			cols = append(cols, "reopen_window_hours")
		}
		if _, err := tx.NewUpdate().Model(t).Column(cols...).WherePK().Exec(ctx); err != nil {
			return errors.Wrap(err, "update tenant general settings")
		}
		if nameChanged {
			if err := activity.Record(ctx, tx, sub, activity.Activity{
				Event: "tenant.renamed", TargetType: "tenant", TargetID: t.ID,
				Properties: map[string]any{"from": fromName, "to": s.Name},
			}); err != nil {
				return err
			}
		}
		if logoChanged {
			if err := activity.Record(ctx, tx, sub, activity.Activity{
				Event: "tenant.logo_updated", TargetType: "tenant", TargetID: t.ID,
			}); err != nil {
				return err
			}
		}
		if reopenChanged {
			if err := activity.Record(ctx, tx, sub, activity.Activity{
				Event: "tenant.reopen_window_updated", TargetType: "tenant", TargetID: t.ID,
				Properties: map[string]any{"from": fromReopen, "to": t.ReopenWindowHours},
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return NewCurrent(a.db, a.files, a.features...).Handle(ctx, sub)
}

// sameUUID 报告两个可选 UUID 指针是否指向相同值。
func sameUUID(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// HTTP 处理 PUT /t/{slug}/api/tenant。
func (a *Rename) HTTP(ctx context.Context, in *RenameInput) (*CurrentOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &CurrentOutput{Body: *dto}, nil
}

package team

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/uptrace/bun"

	"github.com/go-tenon/tenon/activity"
	"github.com/go-tenon/tenon/database"
	"github.com/go-tenon/tenon/errorx"
	"github.com/go-tenon/tenon/identity"
)

// Changes 是新建或修改团队收件箱的请求：名称、说明和分配方式。
type Changes struct {
	Name        string     `json:"name" minLength:"1" maxLength:"60"`
	Description string     `json:"description,omitempty" maxLength:"500"`
	Assignment  Assignment `json:"assignment,omitempty" doc:"分配方式，不填时是手动"`
}

// assignment 返回 c 的分配方式，没有填写时是手动。
func (c Changes) assignment() Assignment {
	if c.Assignment == "" {
		return Manual
	}
	return c.Assignment
}

// CreateInput 是新建团队收件箱的 HTTP 请求。
type CreateInput struct {
	Body Changes
}

// Create 在当前工作区中新建团队收件箱。
type Create struct {
	db *bun.DB
}

// NewCreate 返回新建团队收件箱的动作。
func NewCreate(db *bun.DB) *Create { return &Create{db: db} }

// Handle 在 sub 所在的工作区中新建团队收件箱并记录操作日志。名称已被使用时返回 ErrNameTaken（字段 name）。
func (a *Create) Handle(ctx context.Context, sub identity.Subject, c Changes) (*TeamDTO, error) {
	t := &Team{ID: newID(), TenantID: sub.Tenant, Name: c.Name, Description: c.Description, Assignment: c.assignment()}
	err := database.RunInTx(ctx, a.db, nil, func(ctx context.Context, tx database.Tx) error {
		if _, err := tx.NewInsert().Model(t).Exec(ctx); err != nil {
			if database.IsUniqueViolation(err, "teams_tenant_name") {
				return ErrNameTaken.New(errorx.Field("name"), errorx.Public("name", c.Name))
			}
			return errors.Wrap(err, "insert team")
		}
		return activity.Record(ctx, tx, sub, activity.Activity{Event: "team.created", TargetType: "team", TargetID: t.ID})
	})
	if err != nil {
		return nil, err
	}
	return &TeamDTO{ID: t.ID, Name: t.Name, Description: t.Description, Assignment: t.Assignment}, nil
}

// HTTP 处理 POST /t/{slug}/api/teams。
func (a *Create) HTTP(ctx context.Context, in *CreateInput) (*TeamOutput, error) {
	sub, _ := identity.FromContext(ctx)
	dto, err := a.Handle(ctx, sub, in.Body)
	if err != nil {
		return nil, err
	}
	return &TeamOutput{Body: *dto}, nil
}

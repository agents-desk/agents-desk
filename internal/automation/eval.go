package automation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"agents-desk/internal/channel"
	"agents-desk/internal/contact"
	"agents-desk/internal/conversation"
	"agents-desk/internal/hours"
)

// Fact 是一次自动化求值和执行的上下文快照：惰性加载联系人、渠道和工作时间，按触发事件的发生时刻 At 求值，
// 保证队列重试时判断结果确定，也方便测试控制时间。
type Fact struct {
	db       bun.IDB
	TenantID string
	Conv     *conversation.Conversation
	At       time.Time

	contactLoaded  bool
	contact        *contact.Contact
	channelLoaded  bool
	channel        *channel.Channel
	hoursLoaded    bool
	hours          hours.Hours
	settingsLoaded bool
	hoursSettings  hours.Settings
}

// NewFact 返回会话 conv 在时刻 at 的求值上下文。
func NewFact(db bun.IDB, tenantID string, conv *conversation.Conversation, at time.Time) *Fact {
	return &Fact{db: db, TenantID: tenantID, Conv: conv, At: at}
}

// Contact 返回会话的联系人；会话没有联系人时返回 nil。
func (f *Fact) Contact(ctx context.Context) (*contact.Contact, error) {
	if f.contactLoaded {
		return f.contact, nil
	}
	f.contactLoaded = true
	if f.Conv.ContactID == nil {
		return nil, nil
	}
	c, err := contact.Find(ctx, f.db, f.TenantID, *f.Conv.ContactID)
	if err != nil {
		f.contactLoaded = false
		return nil, err
	}
	f.contact = c
	return c, nil
}

// Channel 返回会话来自的渠道；没有渠道时返回 nil。
func (f *Fact) Channel(ctx context.Context) (*channel.Channel, error) {
	if f.channelLoaded {
		return f.channel, nil
	}
	f.channelLoaded = true
	if f.Conv.ChannelID == nil {
		return nil, nil
	}
	ch, err := channel.Find(ctx, f.db, f.TenantID, *f.Conv.ChannelID)
	if err != nil {
		f.channelLoaded = false
		return nil, err
	}
	f.channel = ch
	return ch, nil
}

// Hours 返回会话所在团队收件箱（或工作区默认）的工作时间。
func (f *Fact) Hours(ctx context.Context) (hours.Hours, error) {
	if f.hoursLoaded {
		return f.hours, nil
	}
	h, err := hours.For(ctx, f.db, f.TenantID, f.Conv.TeamID)
	if err != nil {
		return hours.Hours{}, err
	}
	f.hours, f.hoursLoaded = h, true
	return h, nil
}

// HoursSettings 返回工作区的工作时间设置（包括通常的回复时间）。
func (f *Fact) HoursSettings(ctx context.Context) (hours.Settings, error) {
	if f.settingsLoaded {
		return f.hoursSettings, nil
	}
	s, err := hours.NewGroup(f.db).Tenant(f.TenantID).Get(ctx)
	if err != nil {
		return hours.Settings{}, err
	}
	f.hoursSettings, f.settingsLoaded = s, true
	return s, nil
}

// Field 解析条件中引用的字段值；后续按规则指派扩展渠道、语言、自定义属性时在这里加分支。
func (f *Fact) Field(ctx context.Context, name string) (any, error) {
	switch name {
	case "contact.kind":
		c, err := f.Contact(ctx)
		if err != nil || c == nil {
			return "", err
		}
		return string(c.Kind), nil
	case "contact.email":
		c, err := f.Contact(ctx)
		if err != nil || c == nil {
			return "", err
		}
		return c.Email, nil
	case "channel.kind":
		ch, err := f.Channel(ctx)
		if err != nil || ch == nil {
			return "", err
		}
		return string(ch.Kind), nil
	case "channel.id":
		if f.Conv.ChannelID == nil {
			return "", nil
		}
		return *f.Conv.ChannelID, nil
	case "conversation.language":
		if c, err := f.Contact(ctx); err != nil {
			return "", err
		} else if c != nil && c.Language != "" {
			return c.Language, nil
		}
		return f.Conv.Language, nil
	case "conversation.status":
		return string(f.Conv.Status), nil
	case "hours.open":
		h, err := f.Hours(ctx)
		if err != nil {
			return false, err
		}
		return h.OpenAt(f.At), nil
	}
	return nil, nil
}

// Evaluate 对条件树 cond 求值：All 要求子条件全部为真，Any 要求至少一个为真，空条件恒为真。
func Evaluate(ctx context.Context, cond Condition, fact *Fact) (bool, error) {
	if len(cond.All) > 0 {
		for _, sub := range cond.All {
			ok, err := Evaluate(ctx, sub, fact)
			if err != nil || !ok {
				return false, err
			}
		}
		return true, nil
	}
	if len(cond.Any) > 0 {
		for _, sub := range cond.Any {
			ok, err := Evaluate(ctx, sub, fact)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	}
	if cond.Field == "" {
		return true, nil
	}
	val, err := fact.Field(ctx, cond.Field)
	if err != nil {
		return false, err
	}
	return compare(val, cond.Op, cond.Value), nil
}

// compare 按操作符 op 比较字段值 actual 与期望值 expected。
func compare(actual any, op string, expected any) bool {
	switch op {
	case "eq":
		return equalVal(actual, expected)
	case "neq":
		return !equalVal(actual, expected)
	case "empty":
		return isEmpty(actual)
	case "not_empty":
		return !isEmpty(actual)
	case "in":
		list, ok := expected.([]any)
		if !ok {
			return false
		}
		for _, item := range list {
			if equalVal(actual, item) {
				return true
			}
		}
		return false
	}
	return false
}

// isEmpty 报告值 v 是否为空（nil 或去除空白后的空字符串）。
func isEmpty(v any) bool {
	if v == nil {
		return true
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s) == ""
	}
	return false
}

// equalVal 比较两个条件值是否相等，兼容 JSON 反序列化后的基础类型。
func equalVal(a, b any) bool {
	if a == nil || b == nil {
		return a == b
	}
	if ab, ok := a.(bool); ok {
		bb, ok2 := b.(bool)
		return ok2 && ab == bb
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

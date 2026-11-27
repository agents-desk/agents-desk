-- +goose Up
ALTER TABLE members
    ADD COLUMN ui_language text NOT NULL DEFAULT 'zh-CN';

UPDATE members
SET ui_language = CASE
    WHEN language IN ('zh-CN', 'en') THEN language
    ELSE 'en'
END;

COMMENT ON COLUMN members.ui_language IS '成员的界面显示语言（与前端翻译包一致，如 zh-CN、en）';
COMMENT ON COLUMN members.language IS '成员的沟通与译文偏好语言（任意合法 BCP 47 标签，如 zh-CN、en、hi-Latn、pt-BR）';

ALTER TABLE tickets
    ADD COLUMN derived jsonb NOT NULL DEFAULT '{}'::jsonb;

COMMENT ON COLUMN tickets.derived IS '工单标题与描述的派生翻译数据（JSON）：源语言 language、按目标语言缓存的 title 与 description 译文';

ALTER TABLE contacts
    ADD COLUMN notes_derived jsonb NOT NULL DEFAULT '{}'::jsonb;

COMMENT ON COLUMN contacts.notes_derived IS '联系人长期备注的派生翻译数据（JSON）：源语言 language 与按目标语言缓存的译文 translations';

-- +goose Down
ALTER TABLE contacts
    DROP COLUMN notes_derived;

ALTER TABLE tickets
    DROP COLUMN derived;

COMMENT ON COLUMN members.language IS '界面与译文偏好语言（BCP 47 标签，如 zh-CN、en）';

ALTER TABLE members
    DROP COLUMN ui_language;

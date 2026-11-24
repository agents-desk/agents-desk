import type { ConversationContactConversationDTO } from "@agents-desk/client/widget"
import { Button, Paper, Rating as Stars, Stack, Text, Textarea } from "@mantine/core"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { describeError } from "@go-tenon/client"
import { useT } from "@go-tenon/ui"
import { type ReactNode, useEffect, useRef, useState } from "react"
import { authed, widgetApi } from "../api"
import { useIdentity } from "../realtime"

/** scoreLabels 是每个星数的文字。 */
const scoreLabels = ["rating.score_1", "rating.score_2", "rating.score_3", "rating.score_4", "rating.score_5"] as const

/**
 * RatingCard 是会话关闭后的评价卡片：还没有评价时选 1 到 5 星，选了以后出现意见和提交按钮；评价过以后显示感谢、星数和意见。
 * 提交后用返回的会话更新会话的查询，列表随之刷新。
 */
export function RatingCard({ conversation, color }: { conversation: ConversationContactConversationDTO; color: string }): ReactNode {
  const t = useT()
  const queryClient = useQueryClient()
  const { contact } = useIdentity()
  const [score, setScore] = useState(0)
  const [comment, setComment] = useState("")
  const [submitError, setSubmitError] = useState<string | null>(null)
  const card = useRef<HTMLDivElement>(null)
  // 选了星数后出现意见和提交按钮，把卡片的底部滚到可见的位置
  useEffect(() => {
    if (score > 0) card.current?.scrollIntoView({ block: "end" })
  }, [score])
  const rate = useMutation({
    mutationFn: () => authed(() => widgetApi.conversation.rate(conversation.id, { score, comment: comment.trim() || undefined })),
    onSuccess: (updated) => {
      setSubmitError(null)
      queryClient.setQueryData(["widget", contact?.id, "conversation", conversation.id], updated)
      void queryClient.invalidateQueries({ queryKey: ["widget", contact?.id, "conversations"] })
    },
    onError: (err) => setSubmitError(describeError(err).message ?? t("chat.failed")),
  })

  if (conversation.rating) {
    return (
      <Paper withBorder radius="lg" p="md">
        <Stack gap={6} align="center">
          <Text fw={600} c="green">
            {t("rating.thanks")}
          </Text>
          <Stars value={conversation.rating.score} readOnly size="sm" />
          {conversation.rating.comment && (
            <Text size="sm" c="dimmed" ta="center">
              {conversation.rating.comment}
            </Text>
          )}
        </Stack>
      </Paper>
    )
  }
  return (
    <Paper withBorder radius="lg" p="md" ref={card}>
      <Stack gap="xs" align="center">
        <Text fw={600}>{t("rating.question")}</Text>
        {score === 0 && (
          <Text size="sm" c="dimmed">
            {t("rating.subtitle")}
          </Text>
        )}
        <Stars value={score} onChange={setScore} size="xl" color="yellow" aria-label={t("rating.question")} />
        <Text size="xs" c="dimmed" h={18}>
          {score > 0 ? t(scoreLabels[score - 1]) : ""}
        </Text>
        {score > 0 && (
          <>
            <Textarea
              value={comment}
              onChange={(e) => setComment(e.currentTarget.value)}
              placeholder={t("rating.comment_placeholder")}
              autosize
              minRows={2}
              maxRows={4}
              maxLength={1000}
              size="md"
              w="100%"
            />
            {submitError && (
              <Text size="xs" c="red">
                {submitError}
              </Text>
            )}
            <Button fullWidth color={color} loading={rate.isPending} onClick={() => rate.mutate()}>
              {t("rating.submit")}
            </Button>
          </>
        )}
      </Stack>
    </Paper>
  )
}

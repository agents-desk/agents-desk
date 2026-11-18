import { Container, Stack, Text, Title } from "@mantine/core"
import { createFileRoute } from "@tanstack/react-router"
import { SiteLayout } from "@go-tenon/ui"

export const Route = createFileRoute("/")({
  staticData: { title: "agents-desk" },
  component: Home,
})

// 工作区端的首页：成员登录和工作台在后续的 PR 中加入，现在只是一个占位页。
function Home() {
  return (
    <SiteLayout brand="agents-desk">
      <Container size="sm" py="xl">
        <Stack>
          <Title order={1}>agents-desk</Title>
          <Text c="dimmed">开源、AI 原生的团队沟通和服务平台。工作区端正在建设中。</Text>
        </Stack>
      </Container>
    </SiteLayout>
  )
}

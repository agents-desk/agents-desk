import { Button, Center, Stack, Text, ThemeIcon } from "@mantine/core"
import { createFileRoute, getRouteApi, useNavigate } from "@tanstack/react-router"
import { IconPlus, IconTicket } from "@tabler/icons-react"
import { useCan, useT } from "@go-tenon/ui"
import { useState } from "react"
import { CreateTicketModal } from "../../../../tickets/CreateTicketModal"

// 还没有打开工单时的空态引导页。
export const Route = createFileRoute("/t/$slug/tickets/")({ component: NoTicket })

const ticketsRoute = getRouteApi("/t/$slug/tickets")

function NoTicket() {
  const t = useT()
  const can = useCan()
  const { slug } = Route.useParams()
  const search = ticketsRoute.useSearch()
  const navigate = useNavigate()
  const [createOpen, setCreateOpen] = useState(false)

  return (
    <Center style={{ flex: 1, background: "var(--tn-canvas)" }}>
      <Stack gap="sm" align="center" px="md">
        <ThemeIcon size={48} radius="xl" variant="light" color="gray">
          <IconTicket size={24} stroke={1.5} />
        </ThemeIcon>
        <Stack gap={4} align="center">
          <Text fw={500} size="sm">
            {t("tickets.pick")}
          </Text>
          <Text c="dimmed" size="xs" ta="center">
            {t("tickets.pick_hint")}
          </Text>
        </Stack>
        {can("conversations:reply") && (
          <Button size="xs" variant="light" leftSection={<IconPlus size={14} />} onClick={() => setCreateOpen(true)}>
            {t("tickets.create")}
          </Button>
        )}
      </Stack>
      <CreateTicketModal
        opened={createOpen}
        onClose={() => setCreateOpen(false)}
        onCreated={(created) =>
          void navigate({ to: "/t/$slug/tickets/$id", params: { slug, id: created.id }, search })
        }
      />
    </Center>
  )
}

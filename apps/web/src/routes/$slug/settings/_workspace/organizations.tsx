import type { OrganizationDTO } from "@agents-desk/client/tenant"
import { Button, Group, Modal, Pagination, Stack, Text, TextInput, Textarea } from "@mantine/core"
import { useForm } from "@mantine/form"
import { useDebouncedValue } from "@mantine/hooks"
import { IconPlus, IconSearch, IconTrash } from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute } from "@tanstack/react-router"
import {
  applyServerErrors,
  confirm,
  DataTable,
  FormError,
  notifyError,
  notifySuccess,
  Page,
  useCan,
  useT,
} from "@go-tenon/ui"
import { type ReactNode, useEffect, useState } from "react"
import { tenantApi } from "../../../../api"
import { RowActions } from "../../../../components/row-actions"

const PER_PAGE = 20

// 客户组织管理页：集中维护工作区内的 B2B 客户公司与组织档案。
export const Route = createFileRoute("/$slug/settings/_workspace/organizations")({
  staticData: { title: "nav.organizations", icon: "building", order: 6, permission: "contacts:read" },
  component: OrganizationsPage,
})

function OrganizationsPage(): ReactNode {
  const t = useT()
  const can = useCan()
  const canEdit = can("contacts:update")
  const queryClient = useQueryClient()

  const [keyword, setKeyword] = useState("")
  const [debouncedQ] = useDebouncedValue(keyword.trim(), 250)
  const [page, setPage] = useState(1)
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<OrganizationDTO | null>(null)
  const [formError, setFormError] = useState<ReturnType<typeof applyServerErrors>>()

  useEffect(() => {
    setPage(1)
  }, [debouncedQ])

  const list = useQuery({
    queryKey: ["organization", "list", { page, per_page: PER_PAGE, q: debouncedQ }],
    queryFn: () =>
      tenantApi.organization.list({
        page,
        per_page: PER_PAGE,
        q: debouncedQ || undefined,
      }),
  })

  const form = useForm({
    initialValues: {
      name: "",
      domain: "",
      external_id: "",
      notes: "",
    },
  })

  const closeModal = () => {
    setModalOpen(false)
    setEditing(null)
    setFormError(undefined)
    form.reset()
  }

  const openCreate = () => {
    setEditing(null)
    setFormError(undefined)
    form.setValues({ name: "", domain: "", external_id: "", notes: "" })
    setModalOpen(true)
  }

  const openEdit = (org: OrganizationDTO) => {
    setEditing(org)
    setFormError(undefined)
    form.setValues({
      name: org.name,
      domain: org.domain ?? "",
      external_id: org.external_id ?? "",
      notes: org.notes ?? "",
    })
    setModalOpen(true)
  }

  const invalidateRelated = () => {
    void queryClient.invalidateQueries({ queryKey: ["organization"] })
    void queryClient.invalidateQueries({ queryKey: ["resource", "contact"] })
    void queryClient.invalidateQueries({ queryKey: ["external-groups"] })
  }

  const save = useMutation({
    mutationFn: (values: typeof form.values) => {
      const payload = {
        name: values.name.trim(),
        domain: values.domain.trim(),
        external_id: values.external_id.trim(),
        notes: values.notes.trim(),
      }
      if (editing) {
        return tenantApi.organization.update(editing.id, payload)
      }
      return tenantApi.organization.create({
        name: payload.name,
        domain: payload.domain || undefined,
        external_id: payload.external_id || undefined,
        notes: payload.notes || undefined,
      })
    },
    onSuccess: () => {
      const wasEditing = Boolean(editing)
      closeModal()
      invalidateRelated()
      notifySuccess(t(wasEditing ? "organizations.updated_done" : "organizations.created_done"))
    },
    onError: (err) => {
      setFormError(applyServerErrors(form, err))
    },
  })

  const remove = useMutation({
    mutationFn: (id: string) => tenantApi.organization.delete(id),
    onSuccess: () => {
      invalidateRelated()
      notifySuccess(t("organizations.deleted_done"))
    },
    onError: (err) => notifyError(err),
  })

  const handleDelete = async (org: OrganizationDTO) => {
    const ok = await confirm({
      title: t("organizations.delete_confirm"),
      message: t("organizations.delete_message"),
      danger: true,
      confirmLabel: t("organizations.delete"),
    })
    if (ok) remove.mutate(org.id)
  }

  const records = list.data?.items ?? []
  const total = list.data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / PER_PAGE))

  return (
    <Page
      title={t("nav.organizations")}
      description={t("organizations.description")}
      actions={
        canEdit ? (
          <Button leftSection={<IconPlus size={14} />} onClick={openCreate}>
            {t("organizations.add")}
          </Button>
        ) : undefined
      }
    >
      <Stack gap="md">
        <Group>
          <TextInput
            w={320}
            leftSection={<IconSearch size={14} />}
            placeholder={t("organizations.search_placeholder")}
            value={keyword}
            onChange={(e) => setKeyword(e.currentTarget.value)}
          />
        </Group>

        <DataTable
          records={records}
          loading={list.isPending}
          empty={t("organizations.empty")}
          columns={[
            {
              accessor: "name",
              title: t("organizations.name"),
              render: (row) => (
                <Stack gap={2}>
                  <Text size="sm" fw={500}>
                    {row.name}
                  </Text>
                  {row.notes && (
                    <Text size="xs" c="dimmed" lineClamp={1}>
                      {row.notes}
                    </Text>
                  )}
                </Stack>
              ),
            },
            {
              accessor: "domain",
              title: t("organizations.domain"),
              render: (row) => (
                <Text size="sm" c={row.domain ? undefined : "dimmed"}>
                  {row.domain || "—"}
                </Text>
              ),
            },
            {
              accessor: "external_id",
              title: t("organizations.external_id"),
              render: (row) => (
                <Text size="sm" c={row.external_id ? undefined : "dimmed"}>
                  {row.external_id || "—"}
                </Text>
              ),
            },
            {
              accessor: "contact_count",
              title: t("organizations.contacts_count"),
              render: (row) => <Text size="sm">{row.contact_count}</Text>,
            },
            {
              accessor: "external_group_count",
              title: t("organizations.groups_count"),
              render: (row) => <Text size="sm">{row.external_group_count}</Text>,
            },
            ...(canEdit
              ? [
                  {
                    accessor: "actions",
                    title: "",
                    textAlign: "right" as const,
                    render: (row: OrganizationDTO) => (
                      <RowActions
                        editLabel={t("common.edit")}
                        onEdit={() => openEdit(row)}
                        actions={[
                          {
                            label: t("common.delete"),
                            icon: <IconTrash size={14} />,
                            danger: true,
                            onClick: () => void handleDelete(row),
                          },
                        ]}
                      />
                    ),
                  },
                ]
              : []),
          ]}
        />

        {total > PER_PAGE && (
          <Group justify="flex-end">
            <Pagination value={page} onChange={setPage} total={totalPages} />
          </Group>
        )}
      </Stack>

      <Modal
        opened={modalOpen}
        onClose={closeModal}
        title={t(editing ? "organizations.edit_title" : "organizations.create_title")}
        size="md"
      >
        <form
          noValidate
          onSubmit={form.onSubmit((values) => {
            setFormError(undefined)
            save.mutate(values)
          })}
        >
          <Stack gap="md">
            <FormError error={formError} />
            <TextInput
              label={t("organizations.name")}
              placeholder={t("organizations.name_placeholder")}
              required
              maxLength={120}
              {...form.getInputProps("name")}
            />
            <TextInput
              label={t("organizations.domain")}
              placeholder={t("organizations.domain_placeholder")}
              maxLength={255}
              {...form.getInputProps("domain")}
            />
            <TextInput
              label={t("organizations.external_id")}
              placeholder={t("organizations.external_id_placeholder")}
              maxLength={120}
              {...form.getInputProps("external_id")}
            />
            <Textarea
              label={t("organizations.notes")}
              placeholder={t("organizations.notes_placeholder")}
              minRows={3}
              maxLength={2000}
              {...form.getInputProps("notes")}
            />
            <Group justify="flex-end">
              <Button variant="default" onClick={closeModal}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" loading={save.isPending}>
                {t("common.save")}
              </Button>
            </Group>
          </Stack>
        </form>
      </Modal>
    </Page>
  )
}

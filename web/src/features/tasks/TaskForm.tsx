import { zodResolver } from '@hookform/resolvers/zod'
import { useId, useState } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'
import { apiFetch } from '../../api/client'
import type { ApiError } from '../../api/errors'
import { classifyError } from '../../api/errors'
import { Button } from '../../components/Button'
import { Select } from '../../components/Select'
import { TextField } from '../../components/TextField'
import styles from './TaskForm.module.css'
import type { Task } from './useTasks'

// Mirrors docs/openapi.yaml's CreateTaskRequest/UpdateTaskRequest
// constraints exactly.
const taskFormSchema = z.object({
  title: z.string().trim().min(1, 'Title is required').max(200, 'Must be at most 200 characters'),
  description: z.string().max(2000, 'Must be at most 2000 characters'),
  // '' means "not specified" — see the doc comment on buildRequestBody
  // below for why it's never actually sent to the server as "".
  priority: z.enum(['', 'low', 'medium', 'high']),
})

type TaskFormValues = z.infer<typeof taskFormSchema>

export interface TaskFormProps {
  /** Present → edit this task. Absent → create a new one. */
  task?: Task
  onSuccess: (task: Task) => void
  onCancel: () => void
}

/**
 * "" in priority is the server's own "not provided" sentinel (see
 * docs/openapi.yaml's CreateTaskRequest/UpdateTaskRequest) — sending it
 * explicitly would work identically to omitting the field, but this
 * client never relies on that equivalence: a field the user didn't
 * touch is left out of the body entirely, not sent as an empty string
 * pretending to mean something.
 */
function buildRequestBody(values: TaskFormValues): {
  title: string
  description: string
  priority?: 'low' | 'medium' | 'high'
} {
  return {
    title: values.title,
    description: values.description,
    ...(values.priority ? { priority: values.priority } : {}),
  }
}

function messageForError(error: ApiError): string {
  switch (error.kind) {
    case 'invalid_input':
      return error.message
    case 'conflict':
      // No diff UI — Task.Version is json:"-" server-side (see
      // context.md's finding), so there is no version to compare
      // against. The only honest message is generic: something else
      // changed this task concurrently, not what changed.
      return 'Someone else saved a change to this task at the same time. Please try again.'
    case 'rate_limited':
      return 'Too many attempts. Please wait a moment and try again.'
    default:
      return 'Something went wrong. Please try again.'
  }
}

export function TaskForm({ task, onSuccess, onCancel }: TaskFormProps) {
  const isEditing = task !== undefined
  const [submitError, setSubmitError] = useState<string | null>(null)
  const descriptionId = useId()
  const errorId = useId()
  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<TaskFormValues>({
    resolver: zodResolver(taskFormSchema),
    defaultValues: {
      title: task?.title ?? '',
      description: task?.description ?? '',
      // Editing prefills the task's real current priority (so the
      // select shows what's actually true, and resubmitting it
      // unchanged is just a real value round-tripping, not an empty
      // string standing in for one). Creating defaults to '' — "not
      // specified" only makes sense before a priority exists at all.
      priority: task?.priority ?? '',
    },
  })

  async function onSubmit(values: TaskFormValues) {
    setSubmitError(null)
    const body = buildRequestBody(values)
    try {
      const response = isEditing
        ? await apiFetch(`/v1/tasks/${task.id}`, {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body),
          })
        : await apiFetch('/v1/tasks', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body),
          })
      if (!response.ok) throw response
      onSuccess((await response.json()) as Task)
    } catch (err) {
      const classified = err instanceof Response ? await classifyError(err) : null
      setSubmitError(
        classified ? messageForError(classified) : 'Something went wrong. Please try again.',
      )
    }
  }

  return (
    <form className={styles.form} onSubmit={handleSubmit(onSubmit)} noValidate>
      <TextField label="Title" required error={errors.title?.message} {...register('title')} />
      <div className={styles.field}>
        <label htmlFor={descriptionId} className={styles.label}>
          Description
        </label>
        <textarea
          id={descriptionId}
          className={styles.textarea}
          aria-invalid={errors.description ? true : undefined}
          aria-describedby={errors.description ? errorId : undefined}
          {...register('description')}
        />
        {errors.description && (
          <p id={errorId} className={styles.error} role="alert">
            {errors.description.message}
          </p>
        )}
      </div>
      <Select label="Priority" {...register('priority')}>
        <option value="">
          {isEditing ? 'Leave unchanged' : 'Not specified (defaults to medium)'}
        </option>
        <option value="low">Low</option>
        <option value="medium">Medium</option>
        <option value="high">High</option>
      </Select>
      {submitError && (
        <p className={styles.error} role="alert">
          {submitError}
        </p>
      )}
      <div className={styles.actions}>
        <Button type="button" variant="secondary" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" loading={isSubmitting}>
          {isEditing ? 'Save changes' : 'Create task'}
        </Button>
      </div>
    </form>
  )
}

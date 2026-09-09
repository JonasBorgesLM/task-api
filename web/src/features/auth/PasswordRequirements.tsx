import { CheckIcon, CircleIcon } from '../../components/icons'
import { PASSWORD_REQUIREMENTS } from './passwordStrength'
import styles from './PasswordRequirements.module.css'

interface PasswordRequirementsProps {
  password: string
}

/**
 * Live checklist mirroring user.validatePassword's own accept/reject
 * predicates (see passwordStrength.ts) — issue #219's "medidor de força
 * que espelha a regra do servidor e nunca inventa uma mais frouxa".
 *
 * Purely informational: RegisterPage's schema does not fail validation
 * on any of these, and submitting a password that fails one still
 * reaches the server, which is what actually decides (see that schema's
 * own comment). An empty field shows every requirement unmet rather than
 * mixing an idle "not yet typed anything" state with "typed something
 * that satisfies the not-common/not-predictable checks vacuously" —
 * those two checks are technically true for an empty string, which
 * would otherwise show two green checks before the user has typed a
 * single character.
 */
export function PasswordRequirements({ password }: PasswordRequirementsProps) {
  return (
    <ul className={styles.list} aria-label="Password requirements">
      {PASSWORD_REQUIREMENTS.map((requirement) => {
        const met = password.length > 0 && requirement.met(password)
        return (
          <li key={requirement.id} className={met ? styles.met : styles.unmet}>
            {met ? <CheckIcon /> : <CircleIcon />}
            <span>{requirement.label}</span>
          </li>
        )
      })}
    </ul>
  )
}

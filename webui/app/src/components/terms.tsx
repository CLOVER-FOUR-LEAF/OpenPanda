// SPDX-License-Identifier: AGPL-3.0-or-later

import { useRef } from 'preact/hooks'
import { t } from '../i18n'
import { useModalFocus } from './modal-focus'

/** The license & disclaimer body shared by the onboarding wizard and the
 *  Settings → General "read terms" entry point — ten sections, all strings
 *  live in the onboarding.* i18n keys. */
export function TermsContent() {
  return (
    <div class="wizard-terms">
      {([1, 2, 3, 4, 5, 6, 7, 8, 9, 10] as const).map((n) => (
        <section key={n} class="wizard-terms-section">
          <h3>{t(`onboarding.terms${n}h`)}</h3>
          <p>{t(`onboarding.terms${n}b`)}</p>
        </section>
      ))}
    </div>
  )
}

/** A standalone modal for re-reading the terms after onboarding — same
 *  backdrop/focus-trap contract as the confirm dialog. */
export function TermsModal({ onClose }: { onClose(): void }) {
  const box = useRef<HTMLDivElement>(null)
  useModalFocus(box, true)

  return (
    <div
      class="modal-backdrop"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose()
      }}
    >
      <div
        ref={box}
        class="modal terms-modal"
        role="dialog"
        aria-modal="true"
        aria-label={t('onboarding.termsTitle')}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            e.stopPropagation()
            onClose()
          }
        }}
      >
        <h2 class="modal-title">{t('onboarding.termsTitle')}</h2>
        <TermsContent />
        <div class="modal-actions">
          <button class="btn primary" onClick={onClose}>
            {t('common.close')}
          </button>
        </div>
      </div>
    </div>
  )
}

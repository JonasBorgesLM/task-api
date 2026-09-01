import type { CSSProperties } from 'react'
import styles from './Skeleton.module.css'

export type SkeletonRadius = 'sm' | 'md' | 'lg' | 'full'

export interface SkeletonProps {
  /** CSS width, e.g. "100%", "12rem", 240. */
  width?: string | number
  /** CSS height, e.g. "1rem", 32. */
  height?: string | number
  radius?: SkeletonRadius
  className?: string
}

const radiusVar: Record<SkeletonRadius, string> = {
  sm: 'var(--radius-sm)',
  md: 'var(--radius-md)',
  lg: 'var(--radius-lg)',
  full: 'var(--radius-full)',
}

/**
 * Purely decorative — always aria-hidden. The container it's standing
 * in for owns the real accessibility story (e.g. aria-busy="true" on a
 * list while it loads); a Skeleton claiming its own role/label would
 * announce a meaningless placeholder to a screen reader instead of
 * silently standing in.
 *
 * width/height are the one place in this app's components that takes a
 * literal, non-token value on purpose: a skeleton's whole job is
 * matching the exact shape of the real content it's standing in for
 * (CI-7's "skeleton na forma do conteúdo real"), which is inherently
 * per-instance, not a fixed design-system size.
 */
export function Skeleton({ width, height, radius = 'md', className }: SkeletonProps) {
  const style: CSSProperties = {
    width,
    height,
    borderRadius: radiusVar[radius],
  }

  return (
    <span
      aria-hidden="true"
      className={[styles.skeleton, className].filter(Boolean).join(' ')}
      style={style}
    />
  )
}

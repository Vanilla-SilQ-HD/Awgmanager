<script lang="ts">
	import type { OversizedTag } from '$lib/types';
	import { api } from '$lib/api/client';
	import { notifications } from '$lib/stores/notifications';
	import { Button, ConfirmModal } from '$lib/components/ui';
	import { m, formatLocale } from '$lib/i18n';

	interface Props {
		tags: OversizedTag[];
		maxelem: number;
		/** Вызывается после удаления тега — список нужно перечитать. */
		onremoved?: () => void;
	}

	let { tags, maxelem, onremoved }: Props = $props();

	let pendingRemove = $state<OversizedTag | null>(null);
	let removing = $state(false);

	async function confirmRemove() {
		if (!pendingRemove) return;
		removing = true;
		try {
			await api.deleteHydraRouteOversizedTag(pendingRemove.name);
			pendingRemove = null;
			onremoved?.();
		} catch (e: unknown) {
			// 404 — тега в разделе уже нет (убран в другой вкладке): цель
			// достигнута, просто перечитываем список.
			if ((e as { status?: number }).status === 404) {
				pendingRemove = null;
				onremoved?.();
				return;
			}
			notifications.error(e instanceof Error ? e.message : String(e));
		} finally {
			removing = false;
		}
	}

	function fmtCount(n: number): string {
		if (n < 0) return '?';
		return n.toLocaleString(formatLocale());
	}
</script>

<div class="disabled-pane">
	<header class="pane-header">
		<h2>{m.hrneo_disabled_tags_title()}</h2>
		<span class="pane-meta">{tags.length}</span>
	</header>

	<div class="warn-banner">
		{m.hrneo_disabled_tags_banner({ count: tags.length })}
		<code>IpsetMaxElem = {fmtCount(maxelem)}</code>.
	</div>

	<div class="tag-list">
		{#each tags as t (t.name)}
			<div class="tag-row">
				<span class="tag-name">{t.name}</span>
				<span class="tag-side">
					<span class="tag-count">{m.hrneo_disabled_tags_entries({ count: Math.max(0, t.count), formatted: fmtCount(t.count) })}</span>
					<Button
						variant="secondary"
						size="sm"
						disabled={removing}
						onclick={() => (pendingRemove = t)}
					>
						{m.hrneo_disabled_tags_remove()}
					</Button>
				</span>
			</div>
		{/each}
	</div>
</div>

{#if pendingRemove}
	<ConfirmModal
		open={true}
		title={m.hrneo_disabled_tags_remove_title()}
		message={m.hrneo_disabled_tags_remove_message({ tag: pendingRemove.name })}
		secondary={m.hrneo_disabled_tags_remove_secondary()}
		confirmLabel={m.hrneo_disabled_tags_remove()}
		busy={removing}
		onConfirm={confirmRemove}
		onClose={() => (pendingRemove = null)}
	/>
{/if}

<style>
	.disabled-pane {
		display: flex;
		flex-direction: column;
		gap: 12px;
	}

	.pane-header {
		display: flex;
		align-items: baseline;
		gap: 10px;
		padding-bottom: 10px;
		border-bottom: 1px solid var(--border);
	}
	.pane-header h2 {
		margin: 0;
		font-size: 1.0625rem;
		color: var(--text-primary);
	}
	.pane-meta {
		color: var(--text-muted);
		font-size: 0.8125rem;
	}

	.warn-banner {
		background: rgba(224, 175, 104, 0.1);
		border-left: 3px solid var(--warning);
		color: var(--text-primary);
		padding: 10px 12px;
		border-radius: 0 6px 6px 0;
		font-size: 0.8125rem;
	}
	.warn-banner code {
		background: var(--bg-tertiary);
		padding: 0 4px;
		border-radius: 3px;
		font-family: ui-monospace, monospace;
		font-size: 0.75rem;
	}

	.tag-list {
		display: flex;
		flex-direction: column;
		gap: 6px;
	}

	.tag-row {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: 8px;
		padding: 10px 12px;
		background: var(--bg-secondary);
		border: 1px solid var(--border);
		border-radius: 6px;
	}

	.tag-side {
		display: flex;
		flex-shrink: 0;
		align-items: center;
		gap: 12px;
	}

	.tag-name {
		/* Оттенок — в --tag-hue: светлая тема берёт его же, а не копию hex. */
		--tag-hue: #bb8bff;
		/* Длинное имя без разделителей переносится, а не выталкивает кнопку. */
		min-width: 0;
		overflow-wrap: anywhere;
		font-family: ui-monospace, monospace;
		font-weight: 600;
		color: var(--tag-hue);
	}

	/* Светлая тема: исходный оттенок на белом — 2.5:1, ниже WCAG AA. */
	:global([data-theme='light']) .tag-name {
		color: color-mix(in srgb, var(--tag-hue) 40%, var(--color-text-primary));
	}

	.tag-count {
		color: var(--text-muted);
		font-size: 0.8125rem;
		font-variant-numeric: tabular-nums;
		white-space: nowrap;
	}
</style>

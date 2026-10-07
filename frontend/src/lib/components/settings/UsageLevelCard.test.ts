import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import UsageLevelCard from './UsageLevelCard.svelte';
import { m } from '$lib/i18n';

describe('UsageLevelCard', () => {
	it('на базовом уровне (карточка «Внешний вид» скрыта) показывает язык и формат даты', () => {
		render(UsageLevelCard, { props: { value: 'basic', saving: false, onSelect: () => {} } });
		expect(screen.getByText(m.settings_language_label())).toBeTruthy();
		expect(screen.getByText(m.settings_date_format_label())).toBeTruthy();
	});

	it('с уровня, где видна карточка «Внешний вид», не дублирует их', () => {
		render(UsageLevelCard, { props: { value: 'advanced', saving: false, onSelect: () => {} } });
		expect(screen.queryByText(m.settings_language_label())).toBeNull();
		expect(screen.queryByText(m.settings_date_format_label())).toBeNull();
	});
});

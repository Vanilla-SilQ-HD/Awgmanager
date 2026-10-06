import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import UsageLevelCard from './UsageLevelCard.svelte';

describe('UsageLevelCard', () => {
	it('на базовом уровне (карточка «Внешний вид» скрыта) показывает язык и формат даты', () => {
		render(UsageLevelCard, { props: { value: 'basic', saving: false, onSelect: () => {} } });
		expect(screen.getByText('Язык интерфейса')).toBeTruthy();
		expect(screen.getByText('Формат даты и времени')).toBeTruthy();
	});

	it('с уровня, где видна карточка «Внешний вид», не дублирует их', () => {
		render(UsageLevelCard, { props: { value: 'advanced', saving: false, onSelect: () => {} } });
		expect(screen.queryByText('Язык интерфейса')).toBeNull();
		expect(screen.queryByText('Формат даты и времени')).toBeNull();
	});
});

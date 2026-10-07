import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import LogRow from './LogRow.svelte';

const log = {
	timestamp: '2026-10-07T10:00:00Z',
	level: 'warn',
	group: 'tunnel',
	subgroup: 'ops',
	action: 'start',
	target: 't1',
	message: 'msg'
};

// Enter/пробел на чипах уровня и области — их нажатие (фильтр), а не
// раскрытие строки: обработчик строки не должен перехватывать их клавиши.
describe('LogRow: клавиатура на чипах', () => {
	it('Enter на чипе не раскрывает строку и не гасит нажатие кнопки', async () => {
		const onToggleExpand = vi.fn();
		const { container } = render(LogRow, { props: { log, onToggleExpand } });
		for (const chip of container.querySelectorAll<HTMLButtonElement>('.level-chip, .scope-chip')) {
			expect(await fireEvent.keyDown(chip, { key: 'Enter' })).toBe(true);
		}
		expect(onToggleExpand).not.toHaveBeenCalled();
	});

	it('Enter на строке раскрывает её', async () => {
		const onToggleExpand = vi.fn();
		const { container } = render(LogRow, { props: { log, onToggleExpand } });
		const row = container.querySelector('.row') as HTMLElement;
		expect(await fireEvent.keyDown(row, { key: 'Enter' })).toBe(false);
		expect(onToggleExpand).toHaveBeenCalledOnce();
	});
});

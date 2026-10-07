import { describe, it, expect, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import TerminalCredentialsBar from './TerminalCredentialsBar.svelte';

// «Запомнить» + «Сохранить» сворачивает карточку, как и загрузка уже
// сохранённых данных. Мутация «open не сбрасывается» → красный.
describe('TerminalCredentialsBar: сворачивание после сохранения', () => {
	beforeEach(() => sessionStorage.clear());

	it('с «Запомнить» — свёрнута', async () => {
		const { container } = render(TerminalCredentialsBar);
		const details = container.querySelector('details')!;
		await fireEvent.input(screen.getByPlaceholderText('root'), { target: { value: 'root' } });
		await fireEvent.input(container.querySelector('input[type="checkbox"]')!, { target: { checked: true } });
		await fireEvent.click(screen.getByText('Сохранить'));
		expect(details.open).toBe(false);
	});

	it('без «Запомнить» — остаётся раскрытой', async () => {
		const { container } = render(TerminalCredentialsBar);
		const details = container.querySelector('details')!;
		await fireEvent.input(screen.getByPlaceholderText('root'), { target: { value: 'root' } });
		await fireEvent.click(screen.getByText('Сохранить'));
		expect(details.open).toBe(true);
	});
});

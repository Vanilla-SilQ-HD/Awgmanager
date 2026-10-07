import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import ObfuscatorRelayCard from './ObfuscatorRelayCard.svelte';

describe('ObfuscatorRelayCard', () => {
	it('переключатель шлёт process=true при выключении ядра', async () => {
		const ontoggle = vi.fn();
		render(ObfuscatorRelayCard, { props: { process: false, ontoggle } });
		await fireEvent.click(screen.getByRole('checkbox'));
		expect(ontoggle).toHaveBeenCalledWith(true);
	});
	it('показывает причину срабатывания сторожа', () => {
		render(ObfuscatorRelayCard, { props: { process: true, tripped: 'oops в awgm_relay: epc relay_stop', ontoggle: () => {} } });
		expect(screen.getByText(/oops в awgm_relay/)).toBeTruthy();
	});
	it('не остаётся переключённым, пока страница не сохранила новое значение', async () => {
		const { rerender } = render(ObfuscatorRelayCard, { props: { process: false, ontoggle: () => {} } });
		const box = screen.getByRole<HTMLInputElement>('checkbox');
		expect(box.checked).toBe(true);
		// Сохранение не удалось — проп process не изменился.
		await fireEvent.click(box);
		expect(box.checked).toBe(true);
		// Сохранение прошло — страница передала новое значение.
		await rerender({ process: true, ontoggle: () => {} });
		expect(box.checked).toBe(false);
	});
});

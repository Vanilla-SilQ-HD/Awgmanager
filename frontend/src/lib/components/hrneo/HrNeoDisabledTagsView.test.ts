import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import HrNeoDisabledTagsView from './HrNeoDisabledTagsView.svelte';
import { api } from '$lib/api/client';
import { notifications } from '$lib/stores/notifications';

vi.mock('$lib/api/client', () => ({
	api: { deleteHydraRouteOversizedTag: vi.fn() },
}));
vi.mock('$lib/stores/notifications', () => ({
	notifications: { error: vi.fn() },
}));

const tags = [
	{ name: 'geoip:ru-blocked', count: 82411, file: '/opt/etc/HydraRoute/geoip.dat' },
	{ name: 'geoip:cn', count: -1, file: '' },
];

describe('HrNeoDisabledTagsView', () => {
	beforeEach(() => vi.clearAllMocks());

	it('«Убрать» спрашивает подтверждение, удаляет тег и сообщает об этом', async () => {
		vi.mocked(api.deleteHydraRouteOversizedTag).mockResolvedValue();
		const onremoved = vi.fn();
		render(HrNeoDisabledTagsView, { props: { tags, maxelem: 65536, onremoved } });

		await fireEvent.click(screen.getAllByRole('button', { name: 'Убрать' })[0]);
		expect(api.deleteHydraRouteOversizedTag).not.toHaveBeenCalled();
		expect(screen.getByText('Убрать «geoip:ru-blocked» из отключённых тегов?')).toBeTruthy();

		const dialog = screen.getByRole('dialog');
		const confirm = Array.from(dialog.querySelectorAll('button')).find((b) => b.textContent?.trim() === 'Убрать');
		await fireEvent.click(confirm!);

		await waitFor(() => expect(onremoved).toHaveBeenCalledOnce());
		expect(api.deleteHydraRouteOversizedTag).toHaveBeenCalledWith('geoip:ru-blocked');
		await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
	});

	it('ошибка удаления показывается, окно остаётся открытым', async () => {
		vi.mocked(api.deleteHydraRouteOversizedTag).mockRejectedValue(new Error('boom'));
		const onremoved = vi.fn();
		render(HrNeoDisabledTagsView, { props: { tags, maxelem: 65536, onremoved } });

		await fireEvent.click(screen.getAllByRole('button', { name: 'Убрать' })[1]);
		const dialog = screen.getByRole('dialog');
		const confirm = Array.from(dialog.querySelectorAll('button')).find((b) => b.textContent?.trim() === 'Убрать');
		await fireEvent.click(confirm!);

		await waitFor(() => expect(notifications.error).toHaveBeenCalledWith('boom'));
		expect(onremoved).not.toHaveBeenCalled();
		expect(screen.getByRole('dialog')).toBeTruthy();
	});
});

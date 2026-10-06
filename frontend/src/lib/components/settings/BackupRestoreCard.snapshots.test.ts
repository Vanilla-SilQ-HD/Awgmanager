import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import BackupRestoreCard from './BackupRestoreCard.svelte';
import { api } from '$lib/api/client';

vi.mock('$lib/api/client', () => ({
	api: {
		listUpdateSnapshots: vi.fn(),
		deleteUpdateSnapshot: vi.fn(),
		restoreUpdateSnapshot: vi.fn(),
		downloadUpdateSnapshot: vi.fn()
	}
}));
vi.mock('$lib/stores/notifications', () => ({
	notifications: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn() }
}));

const snap = {
	id: 'before-update-20261006-123045.tar.gz',
	createdAt: '2026-10-06T12:30:45Z',
	appVersion: '2.19.9',
	size: 204800
};

describe('BackupRestoreCard: снимки перед обновлением', () => {
	beforeEach(() => vi.clearAllMocks());

	it('без снимков говорит, когда появится первый', async () => {
		vi.mocked(api.listUpdateSnapshots).mockResolvedValue({ snapshots: [], keep: 3 });
		render(BackupRestoreCard);
		expect(await screen.findByText(/Снимков пока нет/)).toBeTruthy();
		expect(document.body.textContent).toMatch(/хранятся 3 последних/);
	});

	it('показывает версию снимка и удаляет его только после подтверждения', async () => {
		vi.mocked(api.listUpdateSnapshots).mockResolvedValue({ snapshots: [snap], keep: 3 });
		vi.mocked(api.deleteUpdateSnapshot).mockResolvedValue({ snapshots: [], keep: 3 });
		render(BackupRestoreCard);
		expect(await screen.findByText(/версия 2\.19\.9/)).toBeTruthy();

		await fireEvent.click(screen.getByRole('button', { name: 'Удалить' }));
		expect(api.deleteUpdateSnapshot).not.toHaveBeenCalled();
		expect(await screen.findByText('Удалить снимок?')).toBeTruthy();

		const buttons = screen.getAllByRole('button', { name: 'Удалить' });
		await fireEvent.click(buttons[buttons.length - 1]);
		expect(api.deleteUpdateSnapshot).toHaveBeenCalledWith(snap.id);
		expect(await screen.findByText(/Снимков пока нет/)).toBeTruthy();
	});

	it('сбой списка не ломает карточку', async () => {
		vi.mocked(api.listUpdateSnapshots).mockRejectedValue(new Error('boom'));
		render(BackupRestoreCard);
		expect(await screen.findByText(/Снимков пока нет/)).toBeTruthy();
		expect(screen.getByText('Создать копию')).toBeTruthy();
	});
});

import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import IpRoutesTab from './IpRoutesTab.svelte';
import type { StaticRouteList } from '$lib/types';

class ResizeObserverStub {
	observe() {}
	unobserve() {}
	disconnect() {}
}
vi.stubGlobal('ResizeObserver', ResizeObserverStub);

const route: StaticRouteList = {
	id: 'r1',
	name: 'Office',
	tunnelID: 't1',
	subnets: ['10.0.0.0/8'],
	enabled: true,
	createdAt: '',
	updatedAt: ''
};

// Запрос на редактирование из поиска исполняется один раз: обновление списка
// (SSE) после закрытия редактора не должно открывать его снова.
describe('IpRoutesTab: редактирование из поиска', () => {
	it('открывает редактор по запросу и не открывает повторно при обновлении списка', async () => {
		const props = { ipRoutes: [route], routingTunnels: [], editRuleId: 'r1', editRuleCounter: 0 };
		const { rerender } = render(IpRoutesTab, { props });
		expect(screen.queryByRole('dialog')).toBeNull();

		await rerender({ ...props, editRuleCounter: 1 });
		expect(await screen.findByRole('dialog')).toBeTruthy();

		await fireEvent.keyDown(window, { key: 'Escape' });
		await vi.waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());

		await rerender({ ...props, editRuleCounter: 1, ipRoutes: [{ ...route }] });
		expect(screen.queryByRole('dialog')).toBeNull();
	});
});

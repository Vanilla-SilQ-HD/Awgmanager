import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import ConnectionsTable from './ConnectionsTable.svelte';
import type { ConntrackConnection } from '$lib/types';

const conn: ConntrackConnection = {
	protocol: 'tcp',
	src: '192.168.1.10',
	dst: '1.1.1.1',
	srcPort: 50000,
	dstPort: 443,
	state: 'ESTABLISHED',
	packets: 1,
	bytes: 100,
	bytesIn: 50,
	bytesOut: 50,
	ttl: 100,
	routeClass: 'direct',
	interface: 'eth0',
	tunnelId: '',
	tunnelName: '',
	clientMac: '',
	clientName: ''
};

// Enter/пробел на «убить соединение» — нажатие кнопки, а не выбор строки:
// обработчик строки не должен перехватывать их клавиши.
describe('ConnectionsTable: клавиатура на кнопке «убить соединение»', () => {
	function setup() {
		const onSelect = vi.fn();
		const { container } = render(ConnectionsTable, {
			props: {
				connections: [conn],
				group: 'none',
				pagination: { total: 1, offset: 0, limit: 50, returned: 1 },
				selectedKey: null,
				sortBy: '',
				sortDir: 'desc',
				onSortChange: () => {},
				onPageChange: () => {},
				onSelect,
				onKill: () => {}
			}
		});
		const row = container.querySelector('.row[role="button"]') as HTMLElement;
		const kill = row.querySelector('.kill-btn') as HTMLButtonElement;
		return { onSelect, row, kill };
	}

	it('Enter на кнопке не выбирает строку и не гасит нажатие кнопки', async () => {
		const { onSelect, kill } = setup();
		expect(await fireEvent.keyDown(kill, { key: 'Enter' })).toBe(true);
		expect(onSelect).not.toHaveBeenCalled();
	});

	it('Enter на строке выбирает её', async () => {
		const { onSelect, row } = setup();
		expect(await fireEvent.keyDown(row, { key: 'Enter' })).toBe(false);
		expect(onSelect).toHaveBeenCalledOnce();
	});
});

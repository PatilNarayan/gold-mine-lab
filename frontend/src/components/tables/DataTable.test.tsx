import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { DataTable, type Column } from './DataTable';

interface Row {
  id: string;
  name: string;
  value: number;
}

const columns: Column<Row>[] = [
  { key: 'name', header: 'Name', cell: (r) => r.name },
  { key: 'value', header: 'Value', align: 'right', cell: (r) => r.value.toFixed(2), sortValue: (r) => r.value },
];

const rows: Row[] = [
  { id: '1', name: 'alpha', value: 3 },
  { id: '2', name: 'beta', value: 1 },
];

describe('DataTable', () => {
  it('renders rows', () => {
    render(<DataTable columns={columns} rows={rows} rowKey={(r) => r.id} />);
    expect(screen.getByText('alpha')).toBeInTheDocument();
    expect(screen.getByText('beta')).toBeInTheDocument();
  });

  it('shows a loading state', () => {
    render(<DataTable columns={columns} rows={undefined} rowKey={(r) => r.id} isLoading />);
    expect(screen.getAllByRole('status').length).toBeGreaterThan(0);
  });

  it('shows an error state with retry', async () => {
    const onRetry = vi.fn();
    render(
      <DataTable
        columns={columns}
        rows={undefined}
        rowKey={(r) => r.id}
        error={new Error('boom')}
        onRetry={onRetry}
      />,
    );
    expect(screen.getByRole('alert')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: /retry/i }));
    expect(onRetry).toHaveBeenCalledOnce();
  });

  it('shows an empty state', () => {
    render(
      <DataTable
        columns={columns}
        rows={[]}
        rowKey={(r) => r.id}
        emptyTitle="No rows"
        emptyMessage="Try adjusting filters"
      />,
    );
    expect(screen.getByText('No rows')).toBeInTheDocument();
    expect(screen.getByText('Try adjusting filters')).toBeInTheDocument();
  });

  it('sorts rows when a sortable header is clicked', async () => {
    render(<DataTable columns={columns} rows={rows} rowKey={(r) => r.id} />);
    const valueHeader = screen.getByRole('button', { name: /value/i });
    await userEvent.click(valueHeader);
    const cells = screen.getAllByRole('cell').map((c) => c.textContent);
    expect(cells[1]).toBe('1.00'); // beta's value sorted first ascending
  });
});

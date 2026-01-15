import { describe, it, expect, vi, beforeEach } from 'vitest';
import { api } from './api';

// Mock the config store
const updateMock = vi.fn();
vi.mock('../state/config', () => ({
  useConfigStore: {
    getState: vi.fn(() => ({
      apiBaseUrl: '/api',
      apiKey: 'test-key',
      principalId: 'user-123',
      principalRole: 'admin',
      update: updateMock,
    })),
  },
}));

// Mock global fetch
const fetchMock = vi.fn();
vi.stubGlobal('fetch', fetchMock);

describe('API Client', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('listWorkflows calls correct endpoint with headers', async () => {
    fetchMock.mockResolvedValue({
      ok: true,
      json: async () => [{ id: 'wf-1' }],
    });

    const result = await api.listWorkflows();

    expect(fetchMock).toHaveBeenCalled();
    const args = fetchMock.mock.calls[0];
    const url = args[0] as string;
    const options = args[1] as RequestInit;
    const headers = options.headers as Headers;

    expect(url).toContain('/api/v1/workflows');
    expect(headers.get('Accept')).toBe('application/json');
    expect(headers.get('X-API-Key')).toBe('test-key');
    expect(headers.get('X-Principal-Id')).toBe('user-123');
    
    expect(result).toHaveLength(1);
  });

  it('handle error response', async () => {
     fetchMock.mockResolvedValue({
      ok: false,
      status: 500,
      text: async () => 'Internal Server Error',
    });

    await expect(api.listWorkflows()).rejects.toThrow('Internal Server Error');
  });

  it('clears api key on unauthorized', async () => {
    fetchMock.mockResolvedValue({
      ok: false,
      status: 401,
      text: async () => 'unauthorized',
    });

    await expect(api.listWorkflows()).rejects.toThrow('unauthorized');
    expect(updateMock).toHaveBeenCalledWith({ apiKey: '', principalId: '', principalRole: '' });
  });
});

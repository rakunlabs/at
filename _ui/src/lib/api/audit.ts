import axios from 'axios';
import type { ListResult, ListParams } from './types';

const api = axios.create({ baseURL: 'api/v1' });

export interface AuditEntry {
  id: string;
  organization_id: string;
  actor_type: string;
  actor_id: string;
  action: string;
  resource_type: string;
  resource_id: string;
  details: Record<string, any>;
  created_at: string;
}

export async function listAuditEntries(params?: ListParams): Promise<ListResult<AuditEntry>> {
  const res = await api.get<ListResult<AuditEntry>>('/audit', { params });
  return res.data;
}

export async function getAuditTrail(resourceType: string, resourceId: string): Promise<AuditEntry[]> {
  const res = await api.get<AuditEntry[]>(`/audit/${resourceType}/${resourceId}`);
  return res.data;
}

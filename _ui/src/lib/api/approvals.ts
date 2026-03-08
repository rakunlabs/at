import axios from 'axios';
import type { ListResult, ListParams } from './types';

const api = axios.create({ baseURL: 'api/v1' });

export interface Approval {
  id: string;
  organization_id: string;
  type: string;
  status: string;
  requested_by_type: string;
  requested_by_id: string;
  request_details: Record<string, any>;
  decision_note: string;
  decided_by_user_id: string;
  decided_at: string;
  created_at: string;
  updated_at: string;
}

export async function listApprovals(params?: ListParams): Promise<ListResult<Approval>> {
  const res = await api.get<ListResult<Approval>>('/approvals', { params });
  return res.data;
}

export async function createApproval(data: Partial<Approval>): Promise<Approval> {
  const res = await api.post<Approval>('/approvals', data);
  return res.data;
}

export async function listPendingApprovals(): Promise<Approval[]> {
  const res = await api.get<Approval[]>('/approvals/pending');
  return res.data;
}

export async function getApproval(id: string): Promise<Approval> {
  const res = await api.get<Approval>(`/approvals/${id}`);
  return res.data;
}

export async function updateApproval(id: string, data: Partial<Approval>): Promise<Approval> {
  const res = await api.put<Approval>(`/approvals/${id}`, data);
  return res.data;
}

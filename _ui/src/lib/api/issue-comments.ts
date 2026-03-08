import axios from 'axios';

const api = axios.create({ baseURL: 'api/v1' });

export interface IssueComment {
  id: string;
  task_id: string;
  author_type: string;
  author_id: string;
  body: string;
  parent_id: string;
  created_at: string;
  updated_at: string;
}

export async function listCommentsByTask(taskId: string): Promise<IssueComment[]> {
  const res = await api.get<IssueComment[]>(`/tasks/${taskId}/comments`);
  return res.data;
}

export async function getComment(id: string): Promise<IssueComment> {
  const res = await api.get<IssueComment>(`/comments/${id}`);
  return res.data;
}

export async function createComment(taskId: string, data: Partial<IssueComment>): Promise<IssueComment> {
  const res = await api.post<IssueComment>(`/tasks/${taskId}/comments`, data);
  return res.data;
}

export async function updateComment(id: string, data: Partial<IssueComment>): Promise<IssueComment> {
  const res = await api.put<IssueComment>(`/comments/${id}`, data);
  return res.data;
}

export async function deleteComment(id: string): Promise<void> {
  await api.delete(`/comments/${id}`);
}

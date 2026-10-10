import axios from 'axios';

const api = axios.create({
  baseURL: 'api/v1/settings',
});

// ─── Rotate Encryption Key ───

export async function rotateKey(encryptionKey: string): Promise<string> {
  const res = await api.post<{ message: string }>(
    '/rotate-key',
    { encryption_key: encryptionKey }
  );
  return res.data.message;
}

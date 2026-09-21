// Kaykay keys handles by node + id, without direction. Keep its input identity
// unique while preserving the backend's existing bidirectional `data` contract.
const sharedDataPorts = new Set(['edit_fields', 'filter', 'aggregate', 'wait']);
export function canvasInputHandle(type: string, handle: string): string {
  return sharedDataPorts.has(type) && handle === 'data' ? 'data_in' : handle;
}
export function storedInputHandle(type: string, handle: string): string {
  return sharedDataPorts.has(type) && handle === 'data_in' ? 'data' : handle;
}

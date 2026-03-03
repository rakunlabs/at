export interface ListMeta {
  total: number;
  offset: number;
  limit: number;
}

export interface ListResult<T> {
  data: T[];
  meta: ListMeta;
}

export interface ListParams {
  _limit?: number;
  _offset?: number;
  _sort?: string;
  _fields?: string;
  [key: string]: any; // Allow other filter keys
}

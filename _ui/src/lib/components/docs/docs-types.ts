export interface DocsParam {
  name: string;
  type?: string;
  description: string;
  required?: boolean;
}

export interface DocsCodeTab {
  id: string;
  label: string;
  lang: string;
  code: string;
}

export interface SidebarGroup {
  id: string;
  kind: 'api' | 'guides';
  title: string;
  /** Unfiltered entry count, shown as `hits/total` while searching. */
  total: number;
  entries: { id: string; title: string; description: string; body?: string }[];
}

import type { Connection } from '../api/connections';
import type { Connector } from '../api/connectors';

export interface ConnectionSection {
  connector?: Connector;
  provider: string;
  items: Connection[];
}

/** The account list is driven by saved connections, never by the template catalog. */
export function connectionSections(connections: Connection[], connectors: Connector[]): ConnectionSection[] {
  const catalog = new Map(connectors.map(connector => [connector.slug, connector]));
  const sections = new Map<string, ConnectionSection>();
  for (const connection of connections) {
    if (connection.mcp_oauth) continue;
    let section = sections.get(connection.provider);
    if (!section) {
      section = { provider: connection.provider, connector: catalog.get(connection.provider), items: [] };
      sections.set(connection.provider, section);
    }
    section.items.push(connection);
  }
  return [...sections.values()].sort((a, b) =>
    (a.connector?.name || a.provider).localeCompare(b.connector?.name || b.provider));
}

package postgres

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestMCPServerLegacyColumnsMigration(t *testing.T) {
	for _, missing := range [][]string{nil, {"description", "servers", "urls"}, {"servers", "urls"}} {
		name := strings.Join(missing, "-")
		if name == "" {
			name = "current-schema"
		}
		t.Run(name, func(t *testing.T) {
			p, ctx, _, _ := workspaceFixture(t)
			original, err := p.CreateMCPServer(ctx, service.MCPServer{
				Name: "existing", Description: "Stored description", Public: true,
				Config: service.MCPServerConfig{Description: "Legacy config description"},
			})
			if err != nil {
				t.Fatal(err)
			}
			table := p.tableMCPServers.GetTable()
			for _, column := range missing {
				if _, err := p.db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %q DROP COLUMN %q`, table, column)); err != nil {
					t.Fatal(err)
				}
			}
			migration, err := migrationFS.ReadFile("migrations/63_mcp_server_columns.sql")
			if err != nil {
				t.Fatal(err)
			}
			script := strings.ReplaceAll(string(migration), "${TABLE_PREFIX}", strings.TrimSuffix(table, "mcp_servers"))
			// Replaying the repair must also be safe on the current schema.
			for range 2 {
				if _, err := p.db.ExecContext(ctx, script); err != nil {
					t.Fatal(err)
				}
			}
			list, err := p.ListMCPServers(ctx, nil)
			if err != nil || len(list.Data) != 1 || list.Meta.Total != 1 {
				t.Fatalf("list after upgrade: %+v, %v", list, err)
			}
			got, err := p.GetMCPServerByName(ctx, original.Name)
			if err != nil || got == nil {
				t.Fatalf("lookup after upgrade: %+v, %v", got, err)
			}
			wantDescription := original.Description
			if len(missing) == 3 {
				wantDescription = ""
			}
			if got.ID != original.ID || got.WorkspaceID != original.WorkspaceID || !got.Public || got.Config.Description != original.Config.Description || got.Description != wantDescription {
				t.Fatalf("existing record changed: %+v", got)
			}
			got.Description = "Updated description"
			if _, err := p.UpdateMCPServer(ctx, got.ID, *got); err != nil {
				t.Fatal(err)
			}
			got, err = p.GetMCPServer(ctx, original.ID)
			if err != nil || got == nil || got.Description != "Updated description" {
				t.Fatalf("update after upgrade: %+v, %v", got, err)
			}
			if _, err := p.CreateMCPServer(ctx, service.MCPServer{Name: "new", Description: "New description"}); err != nil {
				t.Fatalf("create after upgrade: %v", err)
			}
		})
	}
}

package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/oklog/ulid/v2"

	atcrypto "github.com/rakunlabs/at/internal/crypto"
	"github.com/rakunlabs/at/internal/service"
)

var _ service.WebhookServerStorer = (*Postgres)(nil)

// ─── Encrypted blobs ───

func encodeWebhookBlob(v any, key []byte) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode webhook settings: %w", err)
	}
	if key == nil {
		return string(b), nil
	}
	return atcrypto.Encrypt(string(b), key)
}

func decodeWebhookBlob(raw string, key []byte, out any) error {
	if raw == "" {
		return nil
	}
	if atcrypto.IsEncrypted(raw) {
		var err error
		raw, err = atcrypto.Decrypt(raw, key)
		if err != nil {
			return fmt.Errorf("decrypt webhook settings: %w", err)
		}
	}
	return json.Unmarshal([]byte(raw), out)
}

// storedTriggerSignature decodes a trigger's signing configuration. A value
// that cannot be decrypted is reported as a configured scheme with no secret,
// which fails verification closed rather than silently accepting requests.
func (p *Postgres) storedTriggerSignature(raw string) *service.WebhookSignature {
	if raw == "" {
		return nil
	}
	var sig service.WebhookSignature
	if err := decodeWebhookBlob(raw, p.encKey, &sig); err != nil || sig.Scheme == "" {
		return &service.WebhookSignature{Scheme: service.WebhookSignatureHMAC}
	}
	return &sig
}

// resolveTriggerSignature turns a submitted signature into the stored column.
// Empty scheme clears it; the sentinel secret keeps the stored secret.
func (p *Postgres) resolveTriggerSignature(sig *service.WebhookSignature, storedRaw string) (string, *service.WebhookSignature, error) {
	if sig == nil || strings.TrimSpace(sig.Scheme) == "" {
		return "", nil, nil
	}
	c := *sig
	c.Normalize()
	if c.Secret == service.WebhookSecretSentinel {
		stored := p.storedTriggerSignature(storedRaw)
		if stored == nil || stored.Secret == "" {
			return "", nil, fmt.Errorf("%w: signature secret is required", service.ErrWorkspaceConflict)
		}
		c.Secret = stored.Secret
	}
	if err := c.Validate(); err != nil {
		return "", nil, fmt.Errorf("%w: %w", service.ErrWorkspaceConflict, err)
	}
	raw, err := encodeWebhookBlob(c, p.encKey)
	if err != nil {
		return "", nil, err
	}
	return raw, &c, nil
}

// ─── Trigger routes ───

type triggerRouteRow struct {
	TriggerID string `db:"trigger_id"`
	ServerID  string `db:"server_id"`
	Path      string `db:"path"`
}

func (p *Postgres) attachTriggerRoutes(ctx context.Context, triggers []service.Trigger) error {
	if len(triggers) == 0 {
		return nil
	}
	ids := make([]string, 0, len(triggers))
	for _, t := range triggers {
		ids = append(ids, t.ID)
	}
	var rows []triggerRouteRow
	if err := p.goqu.From(p.workspaceTable("trigger_webhook_routes")).Select("trigger_id", "server_id", "path").
		Where(goqu.C("trigger_id").In(ids), goqu.C("workspace_id").Eq(triggers[0].WorkspaceID)).
		Order(goqu.I("server_id").Asc()).ScanStructsContext(ctx, &rows); err != nil {
		return fmt.Errorf("list trigger webhook routes: %w", err)
	}
	byTrigger := map[string][]service.WebhookRoute{}
	for _, r := range rows {
		byTrigger[r.TriggerID] = append(byTrigger[r.TriggerID], service.WebhookRoute{ServerID: r.ServerID, Path: r.Path})
	}
	for i := range triggers {
		if routes := byTrigger[triggers[i].ID]; routes != nil {
			triggers[i].WebhookRoutes = routes
		}
	}
	return nil
}

func (p *Postgres) singleTriggerWithRoutes(ctx context.Context, row triggerRow) (*service.Trigger, error) {
	t, err := p.triggerRowToRecord(row)
	if err != nil {
		return nil, err
	}
	list := []service.Trigger{*t}
	if err := p.attachTriggerRoutes(ctx, list); err != nil {
		return nil, err
	}
	return &list[0], nil
}

// replaceTriggerRoutes rewrites a trigger's server bindings inside the trigger
// write. Each server must exist, be open to the trigger's workspace, and the
// custom path must be free on that server.
func (p *Postgres) replaceTriggerRoutes(ctx context.Context, w *businessWrite, triggerID string, routes []service.WebhookRoute) error {
	table := p.workspaceTable("trigger_webhook_routes")
	if _, err := w.tx.Delete(table).Where(goqu.Ex{"trigger_id": triggerID, "workspace_id": w.actor.WorkspaceID}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("clear trigger webhook routes: %w", err)
	}
	for _, r := range routes {
		var srv webhookServerRow
		found, err := w.tx.From(p.workspaceTable("webhook_servers")).Select(webhookServerColumns...).
			Where(goqu.Ex{"id": r.ServerID}).ForKeyShare(goqu.Wait).ScanStructContext(ctx, &srv)
		if err != nil {
			return fmt.Errorf("lock webhook server: %w", err)
		}
		if !found {
			return service.ErrAccessResourceNotFound
		}
		if !srv.AllWorkspaces {
			n, err := w.tx.From(p.workspaceTable("webhook_server_workspaces")).Where(goqu.Ex{"server_id": r.ServerID, "workspace_id": w.actor.WorkspaceID}).CountContext(ctx)
			if err != nil {
				return fmt.Errorf("check webhook server workspace: %w", err)
			}
			if n == 0 {
				return service.ErrAccessResourceNotFound
			}
		}
		if _, err := w.tx.Insert(table).Rows(goqu.Record{
			"workspace_id": w.actor.WorkspaceID, "trigger_id": triggerID, "server_id": r.ServerID, "path": r.Path,
		}).Executor().ExecContext(ctx); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return service.ErrWebhookRouteConflict
			}
			return fmt.Errorf("save trigger webhook route: %w", err)
		}
	}
	return nil
}

// ─── Webhook servers ───

type webhookServerRow struct {
	ID            string    `db:"id"`
	Name          string    `db:"name"`
	Description   string    `db:"description"`
	BindHost      string    `db:"bind_host"`
	Port          int       `db:"port"`
	BasePath      string    `db:"base_path"`
	Enabled       bool      `db:"enabled"`
	AllWorkspaces bool      `db:"all_workspaces"`
	Config        string    `db:"config"`
	CreatedAt     time.Time `db:"created_at"`
	UpdatedAt     time.Time `db:"updated_at"`
	CreatedBy     string    `db:"created_by"`
	UpdatedBy     string    `db:"updated_by"`
}

var webhookServerColumns = []any{"id", "name", "description", "bind_host", "port", "base_path", "enabled", "all_workspaces", "config", "created_at", "updated_at", "created_by", "updated_by"}

func (p *Postgres) webhookServerRecord(row webhookServerRow, workspaces []string) (service.WebhookServer, error) {
	s := service.WebhookServer{
		ID: row.ID, Name: row.Name, Description: row.Description, BindHost: row.BindHost, Port: row.Port,
		BasePath: row.BasePath, Enabled: row.Enabled, AllWorkspaces: row.AllWorkspaces, WorkspaceIDs: workspaces,
		CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339),
		CreatedBy: row.CreatedBy, UpdatedBy: row.UpdatedBy,
	}
	if s.WorkspaceIDs == nil {
		s.WorkspaceIDs = []string{}
	}
	var c service.WebhookServerSettings
	if err := decodeWebhookBlob(row.Config, p.encKey, &c); err != nil {
		return s, err
	}
	s.ApplySettings(c)
	if s.AllowedCIDRs == nil {
		s.AllowedCIDRs = []string{}
	}
	return s, nil
}

// webhookServerAdmin requires a live installation administrator. Dedicated
// listeners open ports on the host, which is installation configuration.
func (p *Postgres) webhookServerAdmin(ctx context.Context) (service.AccessPrincipal, error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return a, err
	}
	if !a.PlatformAdmin {
		return a, service.ErrAccessDenied
	}
	return a, nil
}

func (p *Postgres) loadWebhookServers(ctx context.Context, where goqu.Ex) ([]service.WebhookServer, error) {
	p.encKeyMu.RLock()
	defer p.encKeyMu.RUnlock()
	ds := p.goqu.From(p.workspaceTable("webhook_servers")).Select(webhookServerColumns...).Order(goqu.I("name").Asc())
	if where != nil {
		ds = ds.Where(where)
	}
	var rows []webhookServerRow
	if err := ds.ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list webhook servers: %w", err)
	}
	var links []struct {
		ServerID    string `db:"server_id"`
		WorkspaceID string `db:"workspace_id"`
	}
	if err := p.goqu.From(p.workspaceTable("webhook_server_workspaces")).Select("server_id", "workspace_id").
		Order(goqu.I("workspace_id").Asc()).ScanStructsContext(ctx, &links); err != nil {
		return nil, fmt.Errorf("list webhook server workspaces: %w", err)
	}
	byServer := map[string][]string{}
	for _, l := range links {
		byServer[l.ServerID] = append(byServer[l.ServerID], l.WorkspaceID)
	}
	out := make([]service.WebhookServer, 0, len(rows))
	for _, row := range rows {
		s, err := p.webhookServerRecord(row, byServer[row.ID])
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// ListWebhookServers returns the full catalog (TLS key redacted) to an
// installation administrator. Workspace members see only the servers open to
// their selected workspace, without TLS material, address ranges or the list of
// other workspaces — what they need to publish a webhook and copy its URL.
func (p *Postgres) ListWebhookServers(ctx context.Context) ([]service.WebhookServer, error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	all, err := p.loadWebhookServers(ctx, nil)
	if err != nil {
		return nil, err
	}
	out := make([]service.WebhookServer, 0, len(all))
	for _, s := range all {
		if a.PlatformAdmin {
			out = append(out, s.Redacted())
			continue
		}
		if !s.AdmitsWorkspace(a.WorkspaceID) {
			continue
		}
		out = append(out, service.WebhookServer{
			ID: s.ID, Name: s.Name, Description: s.Description, Port: s.Port, BindHost: s.BindHost,
			BasePath: s.BasePath, Enabled: s.Enabled, PublicURL: s.PublicURL,
			TLSCert: tlsMarker(s.TLSEnabled()), WorkspaceIDs: []string{}, AllowedCIDRs: []string{},
		})
	}
	return out, nil
}

// tlsMarker lets a member's UI build an https:// URL without seeing the
// certificate itself.
func tlsMarker(enabled bool) string {
	if enabled {
		return service.WebhookSecretSentinel
	}
	return ""
}

func (p *Postgres) GetWebhookServer(ctx context.Context, id string) (*service.WebhookServer, error) {
	if _, err := p.webhookServerAdmin(ctx); err != nil {
		return nil, err
	}
	list, err := p.loadWebhookServers(ctx, goqu.Ex{"id": id})
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	s := list[0].Redacted()
	return &s, nil
}

// ListRuntimeWebhookServers returns decrypted listener configuration for the
// process that binds the ports. Only boot maintenance authority may read it.
func (p *Postgres) ListRuntimeWebhookServers(ctx context.Context) ([]service.WebhookServer, error) {
	if !service.HasExecutionMaintenance(ctx) {
		return nil, service.ErrExecutionDenied
	}
	return p.loadWebhookServers(ctx, nil)
}

func webhookServerWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			if strings.Contains(pgErr.ConstraintName, "address") {
				return fmt.Errorf("%w: another webhook server already uses that address and port", service.ErrWebhookServerConflict)
			}
			return fmt.Errorf("%w: a webhook server with that name already exists", service.ErrWebhookServerConflict)
		case "23503":
			return fmt.Errorf("%w: unknown workspace", service.ErrWebhookServerConflict)
		}
	}
	return err
}

func (p *Postgres) prepareWebhookServer(ctx context.Context, q workspaceReader, s service.WebhookServer, storedKey string) (service.WebhookServer, string, error) {
	s.Normalize()
	if s.TLSKey == service.WebhookSecretSentinel {
		s.TLSKey = storedKey
	}
	if err := s.Validate(); err != nil {
		return s, "", fmt.Errorf("%w: %w", service.ErrWebhookServerConflict, err)
	}
	if err := s.ValidateTLS(); err != nil {
		return s, "", fmt.Errorf("%w: %w", service.ErrWebhookServerConflict, err)
	}
	for _, ws := range s.WorkspaceIDs {
		n, err := q.From(p.workspaceTable("workspaces")).Where(goqu.Ex{"id": ws, "archived": false}).CountContext(ctx)
		if err != nil {
			return s, "", fmt.Errorf("check webhook server workspace: %w", err)
		}
		if n == 0 {
			return s, "", fmt.Errorf("%w: unknown workspace %q", service.ErrWebhookServerConflict, ws)
		}
	}
	raw, err := encodeWebhookBlob(s.Settings(), p.encKey)
	return s, raw, err
}

func (p *Postgres) writeWebhookServerWorkspaces(ctx context.Context, tx *goqu.TxDatabase, id string, s service.WebhookServer) error {
	table := p.workspaceTable("webhook_server_workspaces")
	if _, err := tx.Delete(table).Where(goqu.Ex{"server_id": id}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("clear webhook server workspaces: %w", err)
	}
	for _, ws := range s.WorkspaceIDs {
		if _, err := tx.Insert(table).Rows(goqu.Record{"server_id": id, "workspace_id": ws}).Executor().ExecContext(ctx); err != nil {
			return webhookServerWriteError(err)
		}
	}
	// Narrowing the audience unpublishes the webhooks of workspaces that lost
	// access; leaving them routed would keep serving traffic the operator
	// just withdrew.
	routes := tx.Delete(p.workspaceTable("trigger_webhook_routes")).Where(goqu.Ex{"server_id": id})
	if !s.AllWorkspaces {
		if len(s.WorkspaceIDs) > 0 {
			routes = routes.Where(goqu.C("workspace_id").NotIn(s.WorkspaceIDs))
		}
		if _, err := routes.Executor().ExecContext(ctx); err != nil {
			return fmt.Errorf("prune webhook routes: %w", err)
		}
	}
	return nil
}

func (p *Postgres) CreateWebhookServer(ctx context.Context, s service.WebhookServer) (*service.WebhookServer, error) {
	a, err := p.webhookServerAdmin(ctx)
	if err != nil {
		return nil, err
	}
	id, err := p.createWebhookServer(ctx, a, s)
	if err != nil {
		return nil, err
	}
	return p.GetWebhookServer(ctx, id)
}

func (p *Postgres) createWebhookServer(ctx context.Context, a service.AccessPrincipal, s service.WebhookServer) (string, error) {
	p.encKeyMu.RLock()
	defer p.encKeyMu.RUnlock()
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin webhook server create: %w", err)
	}
	defer tx.Rollback()
	s, raw, err := p.prepareWebhookServer(ctx, tx, s, "")
	if err != nil {
		return "", err
	}
	id := ulid.Make().String()
	now := time.Now().UTC()
	if _, err := tx.Insert(p.workspaceTable("webhook_servers")).Rows(goqu.Record{
		"id": id, "name": s.Name, "description": s.Description, "bind_host": s.BindHost, "port": s.Port,
		"base_path": s.BasePath, "enabled": s.Enabled, "all_workspaces": s.AllWorkspaces, "config": raw,
		"created_at": now, "updated_at": now, "created_by": a.UserID, "updated_by": a.UserID,
	}).Executor().ExecContext(ctx); err != nil {
		return "", webhookServerWriteError(err)
	}
	if err := p.writeWebhookServerWorkspaces(ctx, tx, id, s); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", webhookServerWriteError(err)
	}
	return id, nil
}

func (p *Postgres) UpdateWebhookServer(ctx context.Context, id string, s service.WebhookServer) (*service.WebhookServer, error) {
	a, err := p.webhookServerAdmin(ctx)
	if err != nil {
		return nil, err
	}
	found, err := p.updateWebhookServer(ctx, a, id, s)
	if err != nil || !found {
		return nil, err
	}
	return p.GetWebhookServer(ctx, id)
}

func (p *Postgres) updateWebhookServer(ctx context.Context, a service.AccessPrincipal, id string, s service.WebhookServer) (bool, error) {
	p.encKeyMu.RLock()
	defer p.encKeyMu.RUnlock()
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin webhook server update: %w", err)
	}
	defer tx.Rollback()
	var row webhookServerRow
	found, err := tx.From(p.workspaceTable("webhook_servers")).Select(webhookServerColumns...).Where(goqu.Ex{"id": id}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &row)
	if err != nil {
		return false, fmt.Errorf("lock webhook server: %w", err)
	}
	if !found {
		return false, nil
	}
	var stored service.WebhookServerSettings
	if err := decodeWebhookBlob(row.Config, p.encKey, &stored); err != nil {
		return false, err
	}
	s, raw, err := p.prepareWebhookServer(ctx, tx, s, stored.TLSKey)
	if err != nil {
		return false, err
	}
	if _, err := tx.Update(p.workspaceTable("webhook_servers")).Set(goqu.Record{
		"name": s.Name, "description": s.Description, "bind_host": s.BindHost, "port": s.Port,
		"base_path": s.BasePath, "enabled": s.Enabled, "all_workspaces": s.AllWorkspaces, "config": raw,
		"updated_at": time.Now().UTC(), "updated_by": a.UserID,
	}).Where(goqu.Ex{"id": id}).Executor().ExecContext(ctx); err != nil {
		return false, webhookServerWriteError(err)
	}
	if err := p.writeWebhookServerWorkspaces(ctx, tx, id, s); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, webhookServerWriteError(err)
	}
	return true, nil
}

func (p *Postgres) DeleteWebhookServer(ctx context.Context, id string) error {
	if _, err := p.webhookServerAdmin(ctx); err != nil {
		return err
	}
	res, err := p.goqu.Delete(p.workspaceTable("webhook_servers")).Where(goqu.Ex{"id": id}).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("delete webhook server: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return service.ErrAccessResourceNotFound
	}
	return nil
}

func (p *Postgres) rotateWebhookKeys(ctx context.Context, tx *sql.Tx, oldKey, newKey []byte) error {
	type item struct{ table, key, id, column, raw string }
	var items []item
	for _, src := range []struct{ table, key, column string }{
		{"webhook_servers", "id", "config"},
		{"triggers", "id", "webhook_secret"},
	} {
		q, _, err := p.goqu.From(p.workspaceTable(src.table)).Select(goqu.C(src.key), goqu.C(src.column)).Where(goqu.C(src.column).Neq("")).ForUpdate(goqu.Wait).ToSQL()
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, q)
		if err != nil {
			return fmt.Errorf("read %s for rotation: %w", src.table, err)
		}
		for rows.Next() {
			var id, raw string
			if err := rows.Scan(&id, &raw); err != nil {
				rows.Close()
				return err
			}
			items = append(items, item{src.table, src.key, id, src.column, raw})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	for _, it := range items {
		var v json.RawMessage
		if err := decodeWebhookBlob(it.raw, oldKey, &v); err != nil {
			return fmt.Errorf("decrypt %s %s: %w", it.table, it.id, err)
		}
		raw, err := encodeWebhookBlob(v, newKey)
		if err != nil {
			return err
		}
		q, _, err := p.goqu.Update(p.workspaceTable(it.table)).Set(goqu.Record{it.column: raw}).Where(goqu.Ex{it.key: it.id}).ToSQL()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("rotate %s %s: %w", it.table, it.id, err)
		}
	}
	return nil
}

// ─── Routing (machine) ───

type webhookRouteRow struct {
	ID           string         `db:"id"`
	WorkspaceID  string         `db:"workspace_id"`
	WorkflowID   sql.NullString `db:"workflow_id"`
	Alias        sql.NullString `db:"alias"`
	Type         string         `db:"type"`
	Public       bool           `db:"public"`
	Enabled      bool           `db:"enabled"`
	HideFromMain bool           `db:"hide_from_main"`
	Secret       string         `db:"webhook_secret"`
	Config       string         `db:"config"`
}

func (p *Postgres) webhookRouteMatch(row webhookRouteRow) *service.WebhookRouteMatch {
	m := &service.WebhookRouteMatch{
		TriggerID: row.ID, WorkspaceID: row.WorkspaceID, WorkflowID: row.WorkflowID.String, Alias: row.Alias.String,
		Type: row.Type, Public: row.Public, Enabled: row.Enabled, HideFromMain: row.HideFromMain,
		Signature: p.storedTriggerSignature(row.Secret),
	}
	var cfg map[string]any
	_ = json.Unmarshal([]byte(row.Config), &cfg)
	m.Methods = service.WebhookMethodsFromConfig(cfg)
	return m
}

func webhookRouteSelect(t string) []any {
	return []any{goqu.I(t + ".id"), goqu.I(t + ".workspace_id"), goqu.I(t + ".workflow_id"), goqu.I(t + ".alias"), goqu.I(t + ".type"), goqu.I(t + ".public"), goqu.I(t + ".enabled"), goqu.I(t + ".hide_from_main"), goqu.I(t + ".webhook_secret"), goqu.L(`"` + t + `".config::text`).As("config")}
}

// ResolveMainWebhookRoute maps the main /webhooks/{id|alias} route to its
// trigger. Like the gateway MCP route lookup it only returns routing metadata;
// authentication and the execution binding still decide whether it runs.
// Aliases are installation-unique (idx_triggers_alias), so the lookup is
// unambiguous across workspaces.
func (p *Postgres) ResolveMainWebhookRoute(ctx context.Context, idOrAlias string) (*service.WebhookRouteMatch, error) {
	p.encKeyMu.RLock()
	defer p.encKeyMu.RUnlock()
	for _, col := range []string{"id", "alias"} {
		var row webhookRouteRow
		found, err := p.goqu.From(p.tableTriggers.As("t")).Select(webhookRouteSelect("t")...).
			Where(goqu.I("t."+col).Eq(idOrAlias)).ScanStructContext(ctx, &row)
		if err != nil {
			return nil, fmt.Errorf("resolve webhook route: %w", err)
		}
		if found {
			return p.webhookRouteMatch(row), nil
		}
	}
	return nil, nil
}

// ResolveWebhookRoute maps a path on a dedicated server to the trigger bound
// to it: first a custom path, then an alias or ID for a binding with no
// custom path. A trigger whose workspace has lost access to the server is not
// resolved.
func (p *Postgres) ResolveWebhookRoute(ctx context.Context, serverID, path string) (*service.WebhookRouteMatch, error) {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil, nil
	}
	p.encKeyMu.RLock()
	defer p.encKeyMu.RUnlock()
	routes := p.workspaceTable("trigger_webhook_routes").As("r")
	servers := p.workspaceTable("webhook_servers").As("s")
	links := p.workspaceTable("webhook_server_workspaces")
	admitted := goqu.Or(
		goqu.I("s.all_workspaces").IsTrue(),
		goqu.I("r.workspace_id").In(p.goqu.From(links).Select("workspace_id").Where(goqu.C("server_id").Eq(serverID))),
	)
	base := p.goqu.From(routes).
		Join(p.tableTriggers.As("t"), goqu.On(goqu.I("t.id").Eq(goqu.I("r.trigger_id")), goqu.I("t.workspace_id").Eq(goqu.I("r.workspace_id")))).
		Join(servers, goqu.On(goqu.I("s.id").Eq(goqu.I("r.server_id")))).
		Select(webhookRouteSelect("t")...).
		Where(goqu.I("r.server_id").Eq(serverID), admitted)
	candidates := []goqu.Expression{
		goqu.I("r.path").Eq(path),
		goqu.And(goqu.I("r.path").Eq(""), goqu.I("t.alias").Eq(path)),
		goqu.And(goqu.I("r.path").Eq(""), goqu.I("t.id").Eq(path)),
	}
	for _, where := range candidates {
		var rows []webhookRouteRow
		if err := base.Where(where).Limit(2).ScanStructsContext(ctx, &rows); err != nil {
			return nil, fmt.Errorf("resolve webhook server route: %w", err)
		}
		if len(rows) == 1 {
			return p.webhookRouteMatch(rows[0]), nil
		}
		if len(rows) > 1 {
			return nil, service.ErrWebhookRouteConflict
		}
	}
	return nil, nil
}

// ─── Deliveries ───

// RecordWebhookDelivery stores one request outcome and keeps only the latest
// WebhookDeliveryRetention rows for the trigger.
func (p *Postgres) RecordWebhookDelivery(ctx context.Context, d service.WebhookDelivery) error {
	if d.WorkspaceID == "" || d.TriggerID == "" {
		return nil
	}
	if d.ID == "" {
		d.ID = ulid.Make().String()
	}
	if len(d.Error) > 1000 {
		d.Error = d.Error[:1000]
	}
	if len(d.Path) > 500 {
		d.Path = d.Path[:500]
	}
	table := p.workspaceTable("webhook_deliveries")
	if _, err := p.goqu.Insert(table).Rows(goqu.Record{
		"id": d.ID, "workspace_id": d.WorkspaceID, "trigger_id": d.TriggerID, "workflow_id": d.WorkflowID,
		"server_id": d.ServerID, "method": d.Method, "path": d.Path, "status": d.Status, "run_id": d.RunID,
		"error": d.Error, "client_ip": d.ClientIP, "body_bytes": d.BodyBytes, "duration_ms": d.DurationMS,
	}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("record webhook delivery: %w", err)
	}
	keep := p.goqu.From(table).Select("id").Where(goqu.Ex{"workspace_id": d.WorkspaceID, "trigger_id": d.TriggerID}).
		Order(goqu.I("id").Desc()).Limit(service.WebhookDeliveryRetention)
	if _, err := p.goqu.Delete(table).Where(goqu.Ex{"workspace_id": d.WorkspaceID, "trigger_id": d.TriggerID}, goqu.C("id").NotIn(keep)).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("trim webhook deliveries: %w", err)
	}
	return nil
}

// ListWebhookDeliveries returns a trigger's latest deliveries to a caller that
// can read the trigger's workflow.
func (p *Postgres) ListWebhookDeliveries(ctx context.Context, triggerID string, limit int) ([]service.WebhookDelivery, error) {
	t, err := p.GetTrigger(ctx, triggerID)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, service.ErrAccessResourceNotFound
	}
	if limit <= 0 || limit > service.WebhookDeliveryRetention {
		limit = service.WebhookDeliveryRetention
	}
	type row = struct {
		ID          string    `db:"id"`
		WorkspaceID string    `db:"workspace_id"`
		TriggerID   string    `db:"trigger_id"`
		WorkflowID  string    `db:"workflow_id"`
		ServerID    string    `db:"server_id"`
		Method      string    `db:"method"`
		Path        string    `db:"path"`
		Status      int       `db:"status"`
		RunID       string    `db:"run_id"`
		Error       string    `db:"error"`
		ClientIP    string    `db:"client_ip"`
		BodyBytes   int64     `db:"body_bytes"`
		DurationMS  int64     `db:"duration_ms"`
		CreatedAt   time.Time `db:"created_at"`
	}
	var scanned []row
	if err := p.goqu.From(p.workspaceTable("webhook_deliveries")).
		Where(goqu.Ex{"workspace_id": t.WorkspaceID, "trigger_id": t.ID}).
		Order(goqu.I("id").Desc()).Limit(uint(limit)).ScanStructsContext(ctx, &scanned); err != nil {
		return nil, fmt.Errorf("list webhook deliveries: %w", err)
	}
	out := make([]service.WebhookDelivery, 0, len(scanned))
	for _, r := range scanned {
		out = append(out, service.WebhookDelivery{
			ID: r.ID, WorkspaceID: r.WorkspaceID, TriggerID: r.TriggerID, WorkflowID: r.WorkflowID, ServerID: r.ServerID,
			Method: r.Method, Path: r.Path, Status: r.Status, RunID: r.RunID, Error: r.Error, ClientIP: r.ClientIP,
			BodyBytes: r.BodyBytes, DurationMS: r.DurationMS, CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out, nil
}

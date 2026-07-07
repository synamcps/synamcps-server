package models

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
	"time"
)

type Visibility string

const (
	VisibilityPublic   Visibility = "public"
	VisibilityGroup    Visibility = "group"
	VisibilityPersonal Visibility = "personal"
)

type Principal struct {
	UserID         string
	Issuer         string
	Email          string
	EmailVerified  bool
	Groups         []string
	SubjectKey     string
	GroupSubjectKeys []string
	AllowedDomains []string
	Scopes         []string
	AuthSource     string
	TeleportRoles  []string
	TeleportTraits map[string][]string
}

type Session struct {
	SessionID         string
	Principal         Principal
	Transport         string
	CurrentStreams    []string
	LastEventByStream map[string]string
	ExpiresAt         time.Time
}

type WebSession struct {
	SessionID string
	Principal Principal
	CSRFToken string
	ExpiresAt time.Time
}

type APIAccessContext struct {
	Principal        Principal
	AuthMode         string
	GrantedScopes    []string
	TokenID          string
	AccessToken      *AccessToken
	AllowedStorage   []AccessTokenStorage
	AllowedMCPServers []AccessTokenMCPServer
}

type SubjectKind string

const (
	SubjectUser SubjectKind = "user"
	SubjectGroup SubjectKind = "group"
	SubjectAllAuthenticated SubjectKind = "all_authenticated"
)

type SubjectRef struct {
	Kind       SubjectKind `json:"kind"`
	Source     string      `json:"source"`
	Issuer     string      `json:"issuer,omitempty"`
	ExternalID string      `json:"externalId"`
}

func (s SubjectRef) Key() string {
	switch s.Kind {
	case SubjectAllAuthenticated:
		return "all:authenticated"
	case SubjectGroup:
		if s.Source == "internal" || s.Issuer == "" {
			return "group:" + s.Source + ":" + s.ExternalID
		}
		return "group:" + issuerHash(s.Issuer) + ":" + s.ExternalID
	default:
		if s.Source == "internal" || s.Issuer == "" {
			return "user:" + s.Source + ":" + s.ExternalID
		}
		return "user:" + issuerHash(s.Issuer) + ":" + s.ExternalID
	}
}

func SubjectKeyForPrincipal(p Principal) string {
	if p.SubjectKey != "" {
		return p.SubjectKey
	}
	source := p.AuthSource
	if source == "" {
		source = "internal"
	}
	return SubjectRef{Kind: SubjectUser, Source: source, Issuer: p.Issuer, ExternalID: p.UserID}.Key()
}

func GroupSubjectKeysForPrincipal(p Principal) []string {
	if len(p.GroupSubjectKeys) > 0 {
		return append([]string(nil), p.GroupSubjectKeys...)
	}
	source := p.AuthSource
	if source == "" {
		source = "internal"
	}
	out := make([]string, 0, len(p.Groups))
	for _, g := range p.Groups {
		if g == "" {
			continue
		}
		out = append(out, SubjectRef{Kind: SubjectGroup, Source: source, Issuer: p.Issuer, ExternalID: g}.Key())
	}
	return out
}

func issuerHash(issuer string) string {
	sum := sha1.Sum([]byte(strings.TrimRight(issuer, "/")))
	return hex.EncodeToString(sum[:])[:12]
}

type StorageRole string
type StoragePermission string
type AccessMode string
type StorageStatus string
type StorageKind string

const (
	RolePlatformAdmin StorageRole = "platform_admin"
	RoleStorageOwner  StorageRole = "storage_owner"
	RoleStorageAdmin  StorageRole = "storage_admin"
	RoleStorageWriter StorageRole = "storage_writer"
	RoleStorageReader StorageRole = "storage_reader"
	RoleGroupAdmin    StorageRole = "group_admin"

	PermissionStorageRead    StoragePermission = "storage.read"
	PermissionDocumentRead   StoragePermission = "document.read"
	PermissionSearchRead     StoragePermission = "search.read"
	PermissionDocumentCreate StoragePermission = "document.create"
	PermissionDocumentUpdate StoragePermission = "document.update"
	PermissionDocumentDelete StoragePermission = "document.delete"
	PermissionACLManage      StoragePermission = "acl.manage"
	PermissionTokenManage    StoragePermission = "token.policy.manage"
	PermissionStorageDelete  StoragePermission = "storage.delete"

	AccessModeRead      AccessMode = "read"
	AccessModeReadWrite AccessMode = "read_write"
	AccessModeNone      AccessMode = "none"

	StorageStatusActive   StorageStatus = "active"
	StorageStatusArchived StorageStatus = "archived"
	StorageStatusDeleting StorageStatus = "deleting"

	StorageKindKnowledge StorageKind = "knowledge"

	RoleMCPServerOwner MCPServerRole = "mcp_server_owner"
	RoleMCPServerAdmin MCPServerRole = "mcp_server_admin"
	RoleMCPServerUser  MCPServerRole = "mcp_server_user"

	PermissionMCPServerUse    MCPServerPermission = "mcp_server.use"
	PermissionMCPServerManage MCPServerPermission = "mcp_server.manage"
	PermissionMCPServerDelete MCPServerPermission = "mcp_server.delete"

	MCPServerStatusActive   MCPServerStatus = "active"
	MCPServerStatusError    MCPServerStatus = "error"
	MCPServerStatusDisabled MCPServerStatus = "disabled"

	MCPAuthTypeBearer       MCPAuthType = "bearer"
	MCPAuthTypeAPIKey       MCPAuthType = "api_key"
	MCPAuthTypeCustomHeader MCPAuthType = "custom_header"

	MCPTransportHTTP MCPTransportKind = "http"
	MCPTransportSSE  MCPTransportKind = "sse"
	MCPTransportAuto MCPTransportKind = "auto"
)

type MCPServerRole string
type MCPServerPermission string
type MCPServerStatus string
type MCPAuthType string
type MCPTransportKind string

type User struct {
	ID              string    `json:"id"`
	SubjectKey      string    `json:"subjectKey"`
	Source          string    `json:"source"`
	Issuer          string    `json:"issuer,omitempty"`
	ExternalSubject string    `json:"externalSubject"`
	Email           string    `json:"email,omitempty"`
	DisplayName     string    `json:"displayName,omitempty"`
	Status          string    `json:"status"`
	PasswordHash    string    `json:"-"`
	CreatedAt       time.Time `json:"createdAt"`
	LastSeenAt       time.Time `json:"lastSeenAt"`
}

type Group struct {
	ID              string     `json:"id"`
	SubjectKey      string     `json:"subjectKey"`
	Source          string     `json:"source"`
	Issuer          string     `json:"issuer,omitempty"`
	ExternalGroupID string     `json:"externalGroupId,omitempty"`
	Name            string     `json:"name"`
	ManagedBy       string     `json:"managedBy"`
	SyncStatus      string     `json:"syncStatus,omitempty"`
	LastSyncedAt    *time.Time `json:"lastSyncedAt,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
}

type GroupMembership struct {
	GroupID   string     `json:"groupId"`
	UserID    string     `json:"userId"`
	Source    string     `json:"source"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

type Storage struct {
	ID              string        `json:"id"`
	Slug            string        `json:"slug"`
	Name            string        `json:"name"`
	OwnerSubjectKey string        `json:"ownerSubjectKey"`
	Visibility      Visibility    `json:"visibility"`
	DefaultAccess   AccessMode    `json:"defaultAccess"`
	Kind            StorageKind   `json:"storageKind"`
	S3Bucket        string        `json:"s3Bucket,omitempty"`
	S3Prefix        string        `json:"s3Prefix"`
	Status          StorageStatus `json:"status"`
	CreatedAt       time.Time     `json:"createdAt"`
	UpdatedAt       time.Time     `json:"updatedAt"`
	ArchivedAt      *time.Time    `json:"archivedAt,omitempty"`
}

type ACLBinding struct {
	ID         string      `json:"id"`
	StorageID  string      `json:"storageId"`
	SubjectKey string      `json:"subjectKey"`
	Role       StorageRole `json:"role"`
	GrantedBy  string      `json:"grantedBy,omitempty"`
	ExpiresAt  *time.Time  `json:"expiresAt,omitempty"`
	CreatedAt  time.Time   `json:"createdAt"`
}

type RateLimitPolicy struct {
	Enabled           bool `json:"enabled"`
	RequestsPerMinute int  `json:"requestsPerMinute,omitempty"`
	RequestsPerHour   int  `json:"requestsPerHour,omitempty"`
	RequestsPerDay    int  `json:"requestsPerDay,omitempty"`
	Burst              int  `json:"burst,omitempty"`
}

type AccessToken struct {
	ID               string          `json:"id"`
	OwnerSubjectKey  string          `json:"ownerSubjectKey"`
	TokenHash        string          `json:"-"`
	Name             string          `json:"name"`
	Mode             AccessMode      `json:"mode"`
	AllowedPermissions []StoragePermission `json:"allowedPermissions,omitempty"`
	RateLimit        RateLimitPolicy `json:"rateLimit"`
	ExpiresAt        *time.Time      `json:"expiresAt,omitempty"`
	RevokedAt        *time.Time      `json:"revokedAt,omitempty"`
	LastUsedAt        *time.Time      `json:"lastUsedAt,omitempty"`
	CreatedBy        string          `json:"createdBy,omitempty"`
	CreatedAt        time.Time       `json:"createdAt"`
}

type AccessTokenStorage struct {
	TokenID       string   `json:"tokenId"`
	StorageID     string   `json:"storageId"`
	MaxMode       AccessMode `json:"maxMode"`
	ToolAllowlist []string `json:"toolAllowlist,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}

type MCPServer struct {
	ID               string           `json:"id"`
	Slug             string           `json:"slug"`
	Name             string           `json:"name"`
	OwnerSubjectKey  string           `json:"ownerSubjectKey"`
	Transport        MCPTransportKind `json:"transport"`
	URL              string           `json:"url"`
	HeadersJSON      string           `json:"headersJson,omitempty"`
	AuthType         MCPAuthType      `json:"authType"`
	AuthHeaderName   string           `json:"authHeaderName,omitempty"`
	HasAuthSecret    bool             `json:"hasAuthSecret"`
	Status           MCPServerStatus  `json:"status"`
	LastConnectedAt  *time.Time       `json:"lastConnectedAt,omitempty"`
	LastError        string           `json:"lastError,omitempty"`
	CreatedAt        time.Time        `json:"createdAt"`
	UpdatedAt        time.Time        `json:"updatedAt"`
}

type MCPServerACLBinding struct {
	ID         string        `json:"id"`
	ServerID   string        `json:"serverId"`
	SubjectKey string        `json:"subjectKey"`
	Role       MCPServerRole `json:"role"`
	GrantedBy  string        `json:"grantedBy,omitempty"`
	ExpiresAt  *time.Time    `json:"expiresAt,omitempty"`
	CreatedAt  time.Time     `json:"createdAt"`
}

type MCPServerTool struct {
	ServerID        string    `json:"serverId"`
	ToolName        string    `json:"toolName"`
	Description     string    `json:"description,omitempty"`
	InputSchemaJSON string    `json:"inputSchemaJson,omitempty"`
	Enabled         bool      `json:"enabled"`
	DiscoveredAt    time.Time `json:"discoveredAt"`
}

type MCPServerResource struct {
	ServerID     string    `json:"serverId"`
	URI          string    `json:"uri"`
	Name         string    `json:"name,omitempty"`
	MimeType     string    `json:"mimeType,omitempty"`
	Description  string    `json:"description,omitempty"`
	Enabled      bool      `json:"enabled"`
	DiscoveredAt time.Time `json:"discoveredAt"`
}

type MCPServerPrompt struct {
	ServerID            string    `json:"serverId"`
	PromptName          string    `json:"promptName"`
	Description         string    `json:"description,omitempty"`
	ArgumentsSchemaJSON string    `json:"argumentsSchemaJson,omitempty"`
	Enabled             bool      `json:"enabled"`
	DiscoveredAt        time.Time `json:"discoveredAt"`
}

type AccessTokenMCPServer struct {
	TokenID           string    `json:"tokenId"`
	ServerID          string    `json:"serverId"`
	ToolAllowlist     []string  `json:"toolAllowlist,omitempty"`
	ResourceAllowlist []string  `json:"resourceAllowlist,omitempty"`
	PromptAllowlist   []string  `json:"promptAllowlist,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
}

type MCPServerCapabilities struct {
	Server   MCPServer           `json:"server"`
	Tools    []MCPServerTool     `json:"tools"`
	Resources []MCPServerResource `json:"resources"`
	Prompts  []MCPServerPrompt   `json:"prompts"`
}

type EffectiveAccess struct {
	TokenID     string              `json:"tokenId,omitempty"`
	StorageID   string              `json:"storageId"`
	SubjectKey  string              `json:"subjectKey"`
	Permissions []StoragePermission `json:"permissions"`
	Mode        AccessMode          `json:"mode"`
}

type AuditEvent struct {
	ID              string    `json:"id"`
	ActorSubjectKey string    `json:"actorSubjectKey"`
	Action          string    `json:"action"`
	ResourceType    string    `json:"resourceType"`
	ResourceID      string    `json:"resourceId"`
	StorageID       string    `json:"storageId,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

type MCPClientKind string

const (
	MCPClientCursor        MCPClientKind = "cursor"
	MCPClientClaudeDesktop MCPClientKind = "claude_desktop"
	MCPClientClaudeCode    MCPClientKind = "claude_code"
	MCPClientContinue      MCPClientKind = "continue"
	MCPClientCline         MCPClientKind = "cline"
	MCPClientGeneric       MCPClientKind = "generic"
)

type MCPClientTemplate struct {
	Kind                   MCPClientKind `json:"kind"`
	DisplayName            string        `json:"displayName"`
	ConfigFormat           string        `json:"configFormat"`
	SupportsStreamableHTTP bool          `json:"supportsStreamableHttp"`
	SupportsSSE            bool          `json:"supportsSse"`
}

type TokenConnectConfig struct {
	Client         MCPClientKind `json:"client"`
	ServerName     string        `json:"serverName"`
	Transport      string        `json:"transport"`
	ConfigFileName string        `json:"configFileName"`
	ConfigBody     string        `json:"configBody"`
	Instructions   []string      `json:"instructions"`
}

type UsageEvent struct {
	TokenID        string    `json:"tokenId"`
	UserSubjectKey string    `json:"userSubjectKey"`
	StorageID      string    `json:"storageId,omitempty"`
	Tool           string    `json:"tool"`
	Operation      string    `json:"operation"`
	Status         string    `json:"status"`
	LatencyMS      int64     `json:"latencyMs"`
	BytesIn        int64     `json:"bytesIn"`
	BytesOut       int64     `json:"bytesOut"`
	CreatedAt      time.Time `json:"createdAt"`
}

type UsagePoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     int64     `json:"value"`
}

type UsageSeries struct {
	Metric string            `json:"metric"`
	Labels map[string]string `json:"labels"`
	Points []UsagePoint      `json:"points"`
}

const (
	DocumentStatusProcessing = "processing"
	DocumentStatusReady        = "ready"
	DocumentStatusFailed       = "failed"
	DocumentStatusDeleting     = "deleting"
)

type DocumentRecord struct {
	DocID          string     `json:"docId"`
	StorageID      string     `json:"storageId"`
	OwnerID        string     `json:"ownerId"`
	Visibility     Visibility `json:"visibility"`
	GroupIDs       []string   `json:"groupIds"`
	Title          string     `json:"title"`
	MimeType       string     `json:"mimeType"`
	Source         string     `json:"source"`
	SourceURL      string     `json:"sourceUrl,omitempty"`
	SourceHash     string     `json:"sourceHash"`
	S3Key          string     `json:"s3Key,omitempty"`
	SummaryChunkID string     `json:"summaryChunkId,omitempty"`
	Status         string     `json:"status"`
	Body           string     `json:"body,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

type ChunkRecord struct {
	ChunkID         string
	DocID           string
	Ordinal         int
	TextHash        string
	TokenCount      int
	IsSummary       bool
	SummaryModel    string
	EmbeddingModel  string
	Text            string
	EmbeddingVector []float32
}

type VectorPayload struct {
	DocID      string
	StorageID  string
	ChunkID    string
	Visibility Visibility
	OwnerID    string
	GroupIDs   []string
	IsSummary  bool
	Source     string
	SourceURL  string
	S3Key      string
	SourceHash string
}

type PageRequest struct {
	Page          int    `json:"page"`
	PageSize      int    `json:"pageSize"`
	SortBy        string `json:"sortBy,omitempty"`
	SortDirection string `json:"sortDirection,omitempty"`
	StorageID     string `json:"storageId,omitempty"`
	Source        string `json:"source,omitempty"`
	SourceURL     string `json:"sourceUrl,omitempty"`
	SourceURLMode string `json:"sourceUrlMode,omitempty"`

	// Server-side access filters. These are populated by the service layer from
	// the caller's effective access — never from client input (json:"-") — so the
	// catalog can apply authorization and visibility inside the SQL query,
	// keeping pagination counts correct and avoiding per-row permission queries.
	AllowedStorageIDs  []string `json:"-"`
	ApplyVisibility    bool     `json:"-"`
	VisibilityOwnerIDs []string `json:"-"`
	VisibilityGroups   []string `json:"-"`
}

type PaginatedKnowledgeList struct {
	Items    []DocumentRecord `json:"items"`
	Page     int              `json:"page"`
	PageSize int              `json:"pageSize"`
	Total    int64            `json:"total"`
	HasNext  bool             `json:"hasNext"`
}

type SearchRequest struct {
	Query   string      `json:"query"`
	TopK    int         `json:"topK"`
	Filters PageRequest `json:"filters"`
}

type SearchHit struct {
	DocID      string    `json:"docId"`
	Title      string    `json:"title"`
	Snippet    string    `json:"snippet"`
	Score      float64   `json:"score"`
	Visibility Visibility `json:"visibility"`
	Source     string    `json:"source"`
	SourceURL  string    `json:"sourceUrl,omitempty"`
}

package preflightapi

import (
	"time"
)

type APITokenSummary struct {
	ID              string     `json:"id"`
	TeamID          string     `json:"team_id"`
	UserID          string     `json:"user_id,omitempty"`
	SSOConnectionID string     `json:"sso_connection_id,omitempty"`
	Name            string     `json:"name"`
	Scopes          []string   `json:"scopes"`
	LastUsedAt      *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	RevokedAt       *time.Time `json:"revoked_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

type ListAPITokensResponse struct {
	Tokens []APITokenSummary `json:"tokens"`
}

type CreateAPITokenRequest struct {
	Name      string     `json:"name"`
	TeamID    string     `json:"team_id,omitempty"`
	Scopes    []string   `json:"scopes"`
	UserEmail string     `json:"user_email,omitempty"`
	UserName  string     `json:"user_name,omitempty"`
	Role      string     `json:"role,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// Reason is optional operator context retained with the token-created audit
	// event. User-created tokens normally leave it empty.
	Reason string `json:"reason,omitempty"`
	// ExpiresIn is a convenience, relative time-to-live in seconds. When set (and
	// ExpiresAt is not), the server resolves ExpiresAt = now + ExpiresIn. Used to
	// mint short-lived, reason-bound support tokens.
	ExpiresIn *int `json:"expires_in,omitempty"`
}

type CreateAPITokenResponse struct {
	Token  APITokenSummary `json:"token"`
	Secret string          `json:"secret"`
}

type RevokeAPITokenResponse struct {
	Token APITokenSummary `json:"token"`
}

type TeamMemberSummary struct {
	UserID         string     `json:"user_id"`
	Email          string     `json:"email"`
	Name           string     `json:"name"`
	Role           string     `json:"role"`
	RemovedAt      *time.Time `json:"removed_at,omitempty"`
	UserDisabledAt *time.Time `json:"user_disabled_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

type ListTeamMembersResponse struct {
	Members []TeamMemberSummary `json:"members"`
}

type UpdateTeamMemberRequest struct {
	Role string `json:"role"`
}

type TeamInvitationSummary struct {
	ID               string     `json:"id"`
	TeamID           string     `json:"team_id"`
	Email            string     `json:"email"`
	Name             string     `json:"name,omitempty"`
	Role             string     `json:"role"`
	InvitedByUserID  string     `json:"invited_by_user_id,omitempty"`
	InvitedByTokenID string     `json:"invited_by_token_id,omitempty"`
	AcceptedByUserID string     `json:"accepted_by_user_id,omitempty"`
	AcceptedAt       *time.Time `json:"accepted_at,omitempty"`
	RevokedAt        *time.Time `json:"revoked_at,omitempty"`
	ExpiresAt        time.Time  `json:"expires_at"`
	CreatedAt        time.Time  `json:"created_at"`
}

type ListTeamInvitationsResponse struct {
	Invitations []TeamInvitationSummary `json:"invitations"`
}

type CreateTeamInvitationRequest struct {
	Email     string     `json:"email"`
	Name      string     `json:"name,omitempty"`
	Role      string     `json:"role"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type EmailDeliverySummary struct {
	Provider string `json:"provider"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
}

type CreateTeamInvitationResponse struct {
	Invitation    TeamInvitationSummary `json:"invitation"`
	AcceptURL     string                `json:"accept_url"`
	Token         string                `json:"token"`
	EmailDelivery *EmailDeliverySummary `json:"email_delivery,omitempty"`
}

type AcceptTeamInvitationRequest struct {
	Token string `json:"token"`
}

type AcceptTeamInvitationResponse struct {
	TeamID string            `json:"team_id"`
	Role   string            `json:"role"`
	Member TeamMemberSummary `json:"member"`
}

type RevokeTeamInvitationResponse struct {
	Invitation TeamInvitationSummary `json:"invitation"`
}

type AccountPlanSummary struct {
	ID                             string   `json:"id"`
	Name                           string   `json:"name"`
	MonthlyPriceCents              int      `json:"monthly_price_cents"`
	IncludedStandardMinutes        int      `json:"included_standard_minutes"`
	IncludedComputeSlotMinutes     int      `json:"included_compute_slot_minutes"`
	MaxTimeoutSeconds              int      `json:"max_timeout_seconds"`
	MaxActiveRuns                  int      `json:"max_active_runs"`
	MaxActiveComputeSlots          int      `json:"max_active_compute_slots"`
	MaxQueuedRuns                  int      `json:"max_queued_runs"`
	CacheQuotaBytes                int64    `json:"cache_quota_bytes"`
	DockerEnabled                  bool     `json:"docker_enabled"`
	NetworkModes                   []string `json:"network_modes"`
	CanCreateVerification          bool     `json:"can_create_verification"`
	CanWriteProjects               bool     `json:"can_write_projects"`
	CanWriteSecrets                bool     `json:"can_write_secrets"`
	CanPublishPolicies             bool     `json:"can_publish_policies"`
	CanCreateTokens                bool     `json:"can_create_tokens"`
	CanManageMembers               bool     `json:"can_manage_members"`
	CanUseSSO                      bool     `json:"can_use_sso"`
	MaxHumanMembers                int      `json:"max_human_members"`
	MaxActiveInvitations           int      `json:"max_active_invitations"`
	MaxInvitationSendsPerHour      int      `json:"max_invitation_sends_per_hour"`
	MaxProjects                    int      `json:"max_projects"`
	MaxRetainedVerificationSources int      `json:"max_retained_verification_sources"`
	MaxFeedbackSubmissionsPerHour  int      `json:"max_feedback_submissions_per_hour"`
}

type AccountSubscriptionSummary struct {
	ID                 string     `json:"id"`
	PlanID             string     `json:"plan_id"`
	Status             string     `json:"status"`
	CurrentPeriodStart *time.Time `json:"current_period_start,omitempty"`
	CurrentPeriodEnd   *time.Time `json:"current_period_end,omitempty"`
	CancelAt           *time.Time `json:"cancel_at,omitempty"`
}

type AccountUsageSummary struct {
	PeriodStart                 *time.Time `json:"period_start,omitempty"`
	PeriodEnd                   *time.Time `json:"period_end,omitempty"`
	StandardRunnerMinutes       float64    `json:"standard_runner_minutes"`
	ComputeSlotMinutes          float64    `json:"compute_slot_minutes"`
	MonthlyRemainingSlotMinutes float64    `json:"monthly_remaining_slot_minutes"`
	PrepaidRemainingSlotMinutes float64    `json:"prepaid_remaining_slot_minutes"`
	ReservedSlotMinutes         float64    `json:"reserved_slot_minutes"`
	BonusComputeSlotMinutes     float64    `json:"bonus_compute_slot_minutes"`
	CacheGBMonth                float64    `json:"cache_gb_month"`
	ArtifactGBMonth             float64    `json:"artifact_gb_month"`
	OverageCharge               float64    `json:"overage_charge"`
	ManualCredit                float64    `json:"manual_credit"`
	ActiveAPITokens             int        `json:"active_api_tokens"`
	RevokedAPITokens            int        `json:"revoked_api_tokens"`
	ProjectCount                int        `json:"project_count"`
}

type AccountUsageResponse struct {
	TeamID       string                      `json:"team_id"`
	Plan         AccountPlanSummary          `json:"plan"`
	Subscription *AccountSubscriptionSummary `json:"subscription,omitempty"`
	Usage        AccountUsageSummary         `json:"usage"`
}

type CreateBillingCheckoutRequest struct {
	PlanID            string `json:"plan_id"`
	CheckoutAttemptID string `json:"checkout_attempt_id"`
	SuccessURL        string `json:"success_url,omitempty"`
	CancelURL         string `json:"cancel_url,omitempty"`
}

type CreateBillingPortalRequest struct {
	ReturnURL string `json:"return_url,omitempty"`
}

type BillingSessionResponse struct {
	URL string `json:"url"`
}

type PendingBillingCheckout struct {
	State     string     `json:"state"`
	PlanID    string     `json:"plan_id,omitempty"`
	ContextID string     `json:"context_id,omitempty"`
	URL       string     `json:"url,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type BillingCheckoutStatusResponse struct {
	Checkout *PendingBillingCheckout `json:"checkout,omitempty"`
}

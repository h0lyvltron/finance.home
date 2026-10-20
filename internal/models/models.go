package models

type Household struct {
	ID                  int64
	Name                string
	Currency            string
	Timezone            string
	WeekStarts          int
	MonthStartDay       int   // 1–28; mid-month pay cycles (default 1)
	FewerNudges         bool  // global coaching density: fewer Home insights
	ExpectedIncomeCents int64 // monthly expected income for left-to-spend
	SavingsTargetCents  int64 // monthly savings contribution target
}

type Person struct {
	ID           int64
	Slot         string // a | b
	Name         string
	PasswordHash string
}

type Account struct {
	ID            int64
	Name          string
	Type          string // checking|savings|cash|credit|loan
	Owner         string // a|b|joint
	Currency      string
	AnchorBalance int64
	AnchorDate    string
	DisplayOrder  int
	Archived      bool
	Balance       int64 // computed
	// TransferAliases is comma-separated text identifying this account in
	// other accounts' bank descriptions.
	TransferAliases string
}

func (a Account) IsLiability() bool {
	return a.Type == "credit" || a.Type == "loan"
}

type Category struct {
	ID            int64
	Name          string
	Kind          string
	ParentID      *int64
	FixedFlexible string
	Color         string
	ExcludeBudget bool
	Protected     bool
	Archived      bool
	DisplayOrder  int
	ParentName    string
	BudgetMode    string // rollup | independent (parents)
}

type Tag struct {
	ID   int64
	Name string
}

type Payee struct {
	ID                int64
	Name              string
	DefaultCategoryID *int64
	DefaultAccountID  *int64
	LastUsedAt        string
}

type SplitLine struct {
	ID           int64
	CategoryID   int64
	Person       string
	Amount       int64
	Notes        string
	CategoryName string
}

type Transaction struct {
	ID              int64
	AccountID       int64
	AccountName     string
	PayeeID         *int64
	PayeeName       string
	Notes           string
	Amount          int64
	TransactionDate string
	Person          string
	Status          string
	NeedsReview     bool
	CategoryID      *int64
	CategoryName    string
	IsSplit         bool
	TransferID      *int64
	Splits          []SplitLine
	Tags            []Tag
	RunningBalance  int64
	DeletedAt       string
	Suggest         []CategoryRef
}

// CategoryRef is a category offered as a one-tap suggestion.
type CategoryRef struct {
	ID     int64
	Name   string
	Reason string // same payee, similar description, similar amount
}

type UndoOp struct {
	ID      string
	Kind    string
	Payload string
}

type MappingProfile struct {
	ID           int64
	Name         string
	AccountID    *int64
	DateColumn   string
	DateFormat   string
	AmountMode   string
	AmountColumn string
	DebitColumn  string
	CreditColumn string
	PayeeColumn  string
	NotesColumn  string
	InvertSign   bool
	HasHeader    bool
	Delimiter    string
}

type PayeeRule struct {
	ID              int64
	SortOrder       int
	Name            string
	Enabled         bool
	PayeeMatch      string // contains|equals|starts_with
	PayeeValue      string
	AmountMin       *int64
	AmountMax       *int64
	AccountID       *int64
	NotesContains   string
	SetCategoryID   *int64
	SetCategoryName string
	SetPerson       string
	RenamePayee     string
	SetTags         string
}

type ImportBatch struct {
	ID            int64
	AccountID     *int64
	AccountName   string
	ProfileID     *int64
	Filename      string
	Status        string
	Headers       []string
	RowCount      int
	ImportedCount int
	CreatedAt     string
	CommittedAt   string
}

type StagingRow struct {
	ID               int64
	BatchID          int64
	RowIndex         int
	RawJSON          string
	TransactionDate  string
	Amount           int64
	PayeeName        string
	Notes            string
	Fingerprint      string
	Action           string // import|skip|merge|transfer_pair
	MatchTxID        *int64
	MatchSummary     string
	TransferTxID     *int64
	MatchedRuleID    *int64
	MatchedRuleName  string
	CategoryID       *int64
	CategoryName     string
	Person           string
	RenamePayee      string
	TagNames         string
	Exception        string
	DuplicateTxID    *int64
	DuplicateSummary string
	NeedsReview      bool
	Suggest          []CategoryRef
}

type ReviewItem struct {
	Transaction
	RuleHint string
}

// BudgetLine is a resolved monthly budget row (defaults + override + rollover).
type BudgetLine struct {
	CategoryID    int64
	CategoryName  string
	ParentID      *int64
	ParentName    string
	FixedFlexible string
	BudgetMode    string // rollup | independent
	IsParent      bool
	HasChildren   bool
	AmountCents   int64 // effective target after rollover
	BaseCents     int64 // target before rollover carry
	Rollover      bool
	RolloverCarry int64
	SpentCents    int64 // positive magnitude of expense
	Remaining     int64 // Amount - Spent
	ExcludeBudget bool
	Color         string
}

type ScheduleTemplate struct {
	ID                   int64
	Name                 string
	PayeeName            string
	AmountCents          int64
	Kind                 string // expense | income
	AccountID            *int64
	AccountName          string
	CategoryID           *int64
	CategoryName         string
	Frequency            string
	IntervalN            int
	NextDue              string
	AutoMatch            bool
	AmountToleranceCents int64
	DateWindowDays       int
	Active               bool
}

type ScheduleInstance struct {
	ID             int64
	TemplateID     int64
	TemplateName   string
	PayeeName      string
	DueDate        string
	ExpectedAmount int64
	Status         string // upcoming|matched|skipped|overdue
	MatchedTxID    *int64
	Kind           string
	CategoryName   string
	AccountID      *int64
	AccountName    string
	DaysUntil      int
}

type CoachingInsight struct {
	Kind          string // pace|review|bills|positive|goal|spike|card|split
	Tone          string // warn|ok|info
	Title         string
	Detail        string
	Href          string
	Amount        int64 // optional cents for display
	ShowAmt       bool
	SplitCategory int64  // set for a split suggestion
	SplitKey      string // payee key, so dismiss hides this pair only
}

// DraftTransaction is a durable quick-add draft (offline queue / Save draft).
type DraftTransaction struct {
	ID              int64
	PersonID        int64
	AccountID       *int64
	CategoryID      *int64
	PayeeName       string
	Notes           string
	Amount          int64
	TransactionDate string
	Person          string
	Kind            string
	ClientKey       string
	CreatedAt       string
	UpdatedAt       string
	AccountName     string
	CategoryName    string
}

type LeftToSpend struct {
	ExpectedIncome int64
	FixedBills     int64
	FlexibleSpent  int64
	SavingsTarget  int64
	Amount         int64 // income - fixed - flexible - savings
	YearMonth      string
}

type BudgetHealth struct {
	OverCount    int
	UnderCount   int
	OnTrackCount int
	OverCents    int64 // total overspend
	UnderCents   int64 // total underspend
	Budgeted     int64
	Spent        int64
}

type RecurringSuggestion struct {
	PayeeName   string
	AmountCents int64
	Frequency   string // monthly heuristic
	Occurrences int
	LastDate    string
	CategoryID  *int64
	AccountID   *int64
}

// Goal is a savings target or debt payoff tracker (no APR math).
type Goal struct {
	ID             int64
	Kind           string // savings | debt
	Name           string
	TargetCents    int64
	TargetDate     string // YYYY-MM-DD or empty
	PaceWindowDays int
	Archived       bool
	AccountIDs     []int64
	AccountNames   []string
}

// GoalProgress is computed progress for a goal.
type GoalProgress struct {
	Goal             Goal
	CurrentCents     int64 // savings: linked balances; debt: outstanding (positive owed)
	StartWindowCents int64 // balance at start of pace window
	DeltaWindowCents int64 // change over pace window (savings: up good; debt: down good)
	Pct              float64
	OnTrack          bool
	Behind           bool
	WeeklyCatchUp    int64 // positive cents/week when behind (savings contribute / debt pay)
	DaysLeft         int
	WeeksLeft        int
	NeededPerWeek    int64 // required pace to hit date
	RecentPerWeek    int64 // observed pace over window
	MilestoneHit     bool  // crossed 25/50/75/100 recently
}

type BalancePoint struct {
	Date   string
	Label  string
	Amount int64 // net worth or account balance (NW-oriented)
}

type AccountSeries struct {
	AccountID   int64
	AccountName string
	AccountType string
	Points      []BalancePoint
}

type NetWorthSeries struct {
	Points   []BalancePoint
	Accounts []AccountSeries
	Start    int64
	End      int64
	Change   int64
}

type SavingsRatePoint struct {
	Period    string
	Label     string
	Income    int64
	Expense   int64
	Saved     int64
	RatePct   float64
	HasIncome bool
}

type SavingsRateReport struct {
	Income        int64
	Expense       int64
	Saved         int64 // income - expense
	RatePct       float64
	TargetCents   int64 // household monthly savings target (scaled to range when possible)
	TargetRatePct float64
	HasTarget     bool
	VsTargetCents int64 // Saved - TargetCents
	YearMonth     string
	Points        []SavingsRatePoint
}

type MonthlyRecap struct {
	YearMonth       string
	Label           string
	Budgeted        int64
	Spent           int64
	BudgetRemaining int64
	SavingsRate     SavingsRateReport
	NetWorthStart   int64
	NetWorthEnd     int64
	NetWorthChange  int64
	Goals           []GoalProgress
	Wins            []CoachingInsight // up to 2
	Watch           *CoachingInsight  // one watch item
}

// DashboardLayout is a per-person (or household default) ordered widget list.
type DashboardLayout struct {
	ID          int64
	PersonID    *int64 // nil = household default template
	RangePreset string // this_month | last_30 | ytd | last_12
	ViewScope   string // household | a | b
	CreatedAt   string
	UpdatedAt   string
}

// DashboardWidget is one tile on a personal dashboard cell grid.
type DashboardWidget struct {
	ID         int64
	LayoutID   int64
	WidgetType string
	Width      string // s | m | l (initial size hint; grid span is authoritative)
	SortOrder  int    // reading order (row, then column)
	GridCol    int    // 1-based column on the 12-column grid
	GridRow    int    // 1-based row
	GridW      int    // column span
	GridH      int    // row span
	ConfigJSON string
	CreatedAt  string
	Label      string // display label (not persisted)
}

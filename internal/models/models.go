package models

type Household struct {
	ID        int64
	Name      string
	Currency  string
	Timezone  string
	WeekStarts int
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
	ID         int64
	CategoryID int64
	Person     string
	Amount     int64
	Notes      string
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
	ID             int64
	SortOrder      int
	Name           string
	Enabled        bool
	PayeeMatch     string // contains|equals|starts_with
	PayeeValue     string
	AmountMin      *int64
	AmountMax      *int64
	AccountID      *int64
	NotesContains  string
	SetCategoryID  *int64
	SetCategoryName string
	SetPerson      string
	RenamePayee    string
	SetTags        string
}

type ImportBatch struct {
	ID             int64
	AccountID      *int64
	AccountName    string
	ProfileID      *int64
	Filename       string
	Status         string
	Headers        []string
	RowCount       int
	ImportedCount  int
	CreatedAt      string
	CommittedAt    string
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
}

type ReviewItem struct {
	Transaction
	RuleHint string
}

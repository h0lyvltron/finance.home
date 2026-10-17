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

package handlers

import (
	"database/sql"
	"html/template"
	"net/http"
	"strings"

	"finance.home/internal/auth"
	"finance.home/internal/config"
	"finance.home/internal/models"
	"finance.home/internal/store"
	"finance.home/internal/web"
)

type App struct {
	DB     *sql.DB
	Store  *store.Store
	Render *web.Renderer
	Cfg    config.Config
}

type pageData struct {
	Nav          string
	Household    *models.Household
	Session      *auth.Session
	CSRF         string
	Currency     string
	NameA        string
	NameB        string
	Accounts     []models.Account
	Categories   []models.Category
	Payees       []models.Payee
	Tags         []models.Tag
	Transactions []models.Transaction
	Account      *models.Account
	Flash        string
	FlashType    string
	Error        string
	Warn         string
	ShowRunning  bool
	NetWorth     int64
	People       []models.Person
	ReviewCount  int
	// forms / misc
	Form          any
	FormBalance   string
	HouseholdName string
	CurrencySel   string
	Timezone      string
	PersonAName   string
	PersonBName   string
	Name          string
	Items         []models.Transaction
	StatementDate string
	EndingBalance string
	Result        *reconcileResult
	UndoID        string
	Message       string
	// import / rules
	Batches   []models.ImportBatch
	Batch     *models.ImportBatch
	Profiles  []models.MappingProfile
	Rules     []models.PayeeRule
	RuleViews []store.RuleView
	RuleQuery string
	// categories page
	CategoryGroups     []store.CategoryGroup
	ArchivedCategories []store.CategoryView
	Swatches           []string
	// tags
	TagViews         []store.TagView
	ArchivedTagViews []store.TagView
	TxSummary        *txSummaryView
	StagingRows      []models.StagingRow
	StagingGroups    []store.StagingGroup
	DrawerRow        *models.StagingRow
	Headers          []string
	Guess            any
	Page             int
	PageSize         int
	Total            int
	TotalPages       int
	ExceptionsOnly   bool
	ImportedCount    int
	ErrorCount       int
	FirstError       string
	// filters / reports
	Filter       filterView
	GroupBy      string
	Grain        string
	Segments     []store.SegmentTotal
	CashFlow     []store.CashFlowPoint
	Pivot        *store.PivotTable
	ReportTotal  int64
	IncomeTotal  int64
	ExpenseTotal int64
	ChartJSON    template.JS
	ShowChart    bool
	DrillBase    string
	// trends overview
	TrendChartJSON    template.JS
	CategoryChartJSON template.JS
	AvgExpense        int64
	AvgIncome         int64
	PeriodCount       int
	ReviewGroups      []store.ReviewGroup
	UncatCount        int
	UncatSpend        int64
	UncatHref         string
	QueryString       string
	// plan / home (slice 4)
	PlanTab      string
	YearMonth    string
	BudgetLines  []models.BudgetLine
	BudgetHealth models.BudgetHealth
	LeftToSpend  models.LeftToSpend
	Bills        []models.ScheduleInstance
	Schedules    []models.ScheduleTemplate
	Suggestions  []models.RecurringSuggestion
	Insights     []models.CoachingInsight
	// goals / depth reports / recap (slice 5)
	GoalProgress   []models.GoalProgress
	DebtSeries     []models.BalancePoint
	DebtGoalName   string
	NetWorthSeries models.NetWorthSeries
	SavingsRate    models.SavingsRateReport
	MonthlyRecap   models.MonthlyRecap
	// personal dashboards (slice 6)
	PersonalDash store.PersonalDashboard
	DashEdit     bool
	// polish (slice 7)
	CompareMode     string // "" | "ly"
	CompareCashFlow []store.CashFlowPoint
	CompareSegs     []store.SegmentTotal
	CompareIncome   int64
	CompareExpense  int64
	CompareTotal    int64
	SoftDeleted     []store.SoftDeletedTx
	Drafts          []models.DraftTransaction
	CoachingPrefs   []store.CoachingPref
	MonthStartDay   int
	FewerNudges     bool
	RetentionDays   int
	// transfer audit
	Audit          *store.TransferAudit
	OneSidedGroups []oneSidedGroup
}

type reconcileResult struct {
	Difference int64
}

func (a *App) base(r *http.Request, sess *auth.Session) pageData {
	d := pageData{Session: sess, Currency: "USD"}
	if sess != nil {
		d.CSRF = sess.CSRFToken
	}
	h, err := a.Store.Household()
	if err == nil {
		d.Household = h
		d.Currency = h.Currency
	}
	people, _ := a.Store.People()
	for _, p := range people {
		if p.Slot == "a" {
			d.NameA = p.Name
		}
		if p.Slot == "b" {
			d.NameB = p.Name
		}
	}
	d.People = people
	if n, err := a.Store.NeedsReviewCount(); err == nil {
		d.ReviewCount = n
	}
	return d
}

func (a *App) withQuickAdd(d *pageData) {
	accts, _ := a.Store.ListAccounts(false)
	cats, _ := a.Store.ListCategories("", false)
	payees, _ := a.Store.ListPayees(30)
	d.Accounts = accts
	d.Categories = cats
	d.Payees = payees
}

func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /static/", http.StripPrefix("/static/", web.StaticHandler()))
	mux.HandleFunc("GET /manifest.webmanifest", a.manifest)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("GET /setup", a.setupGet)
	mux.HandleFunc("POST /setup", a.setupPost)
	mux.HandleFunc("GET /login", a.loginGet)
	mux.HandleFunc("POST /login", a.loginPost)
	mux.HandleFunc("GET /logout", a.logout)

	mux.HandleFunc("GET /{$}", a.requireAuth(a.home))
	mux.HandleFunc("POST /dashboard/range", a.requireAuth(a.csrf(a.dashboardSetRange)))
	mux.HandleFunc("POST /dashboard/scope", a.requireAuth(a.csrf(a.dashboardSetScope)))
	mux.HandleFunc("POST /dashboard/widgets", a.requireAuth(a.csrf(a.dashboardAddWidget)))
	mux.HandleFunc("POST /dashboard/compact", a.requireAuth(a.csrf(a.dashboardCompact)))
	mux.HandleFunc("POST /dashboard/reset", a.requireAuth(a.csrf(a.dashboardReset)))
	mux.HandleFunc("POST /dashboard/widgets/{id}/place", a.requireAuth(a.csrf(a.dashboardPlaceWidget)))
	mux.HandleFunc("POST /dashboard/widgets/{id}/delete", a.requireAuth(a.csrf(a.dashboardRemoveWidget)))
	mux.HandleFunc("GET /plan", a.requireAuth(a.planPage))
	mux.HandleFunc("POST /plan/settings", a.requireAuth(a.csrf(a.planSettingsPost)))
	mux.HandleFunc("POST /plan/budgets", a.requireAuth(a.csrf(a.planBudgetsPost)))
	mux.HandleFunc("POST /plan/budgets/copy", a.requireAuth(a.csrf(a.planCopyMonth)))
	mux.HandleFunc("POST /schedules", a.requireAuth(a.csrf(a.scheduleCreate)))
	mux.HandleFunc("POST /schedules/{id}/deactivate", a.requireAuth(a.csrf(a.scheduleDeactivate)))
	mux.HandleFunc("POST /schedules/instances/{id}/paid", a.requireAuth(a.csrf(a.scheduleMarkPaid)))
	mux.HandleFunc("POST /schedules/instances/{id}/skip", a.requireAuth(a.csrf(a.scheduleSkip)))
	mux.HandleFunc("POST /schedules/suggest", a.requireAuth(a.csrf(a.scheduleSuggestAccept)))
	mux.HandleFunc("GET /accounts", a.requireAuth(a.accountsList))
	mux.HandleFunc("GET /accounts/new", a.requireAuth(a.accountNew))
	mux.HandleFunc("POST /accounts", a.requireAuth(a.csrf(a.accountCreate)))
	mux.HandleFunc("GET /accounts/{id}", a.requireAuth(a.accountDetail))
	mux.HandleFunc("GET /accounts/{id}/edit", a.requireAuth(a.accountEdit))
	mux.HandleFunc("POST /accounts/{id}", a.requireAuth(a.csrf(a.accountUpdate)))
	mux.HandleFunc("GET /accounts/{id}/reconcile", a.requireAuth(a.reconcileGet))
	mux.HandleFunc("POST /accounts/{id}/reconcile", a.requireAuth(a.csrf(a.reconcilePost)))

	mux.HandleFunc("GET /transactions", a.requireAuth(a.transactionsList))
	mux.HandleFunc("GET /transactions/new", a.requireAuth(a.transactionNew))
	mux.HandleFunc("POST /transactions", a.requireAuth(a.csrf(a.transactionCreate)))
	mux.HandleFunc("POST /transactions/quick", a.requireAuth(a.csrf(a.quickAdd)))
	mux.HandleFunc("POST /transactions/{id}/delete", a.requireAuth(a.csrf(a.transactionDelete)))

	mux.HandleFunc("GET /trends", a.requireAuth(a.trendsPage))
	mux.HandleFunc("GET /explore", a.requireAuth(a.explorePage))
	mux.HandleFunc("GET /reports/cashflow", a.requireAuth(a.cashflowPage))
	mux.HandleFunc("GET /reports/pivot", a.requireAuth(a.pivotPage))
	mux.HandleFunc("GET /reports/networth", a.requireAuth(a.networthPage))
	mux.HandleFunc("GET /reports/budget", a.requireAuth(a.budgetReportPage))
	mux.HandleFunc("GET /reports/savings", a.requireAuth(a.savingsRatePage))
	mux.HandleFunc("GET /goals", a.requireAuth(a.goalsPage))
	mux.HandleFunc("POST /goals", a.requireAuth(a.csrf(a.goalCreate)))
	mux.HandleFunc("POST /goals/{id}", a.requireAuth(a.csrf(a.goalUpdate)))
	mux.HandleFunc("POST /goals/{id}/archive", a.requireAuth(a.csrf(a.goalArchive)))
	mux.HandleFunc("GET /recap", a.requireAuth(a.recapPage))
	mux.HandleFunc("POST /filters/save", a.requireAuth(a.csrf(a.saveFilterPost)))
	mux.HandleFunc("POST /filters/saved/{id}/delete", a.requireAuth(a.csrf(a.deleteSavedFilter)))

	mux.HandleFunc("GET /transfers/new", a.requireAuth(a.transferNew))
	mux.HandleFunc("POST /transfers", a.requireAuth(a.csrf(a.transferCreate)))
	mux.HandleFunc("POST /transfers/quick", a.requireAuth(a.csrf(a.transferQuick)))
	mux.HandleFunc("GET /transfers/audit", a.requireAuth(a.transferAudit))
	mux.HandleFunc("POST /transfers/audit/link", a.requireAuth(a.csrf(a.transferAuditLink)))
	mux.HandleFunc("POST /transfers/audit/link-all", a.requireAuth(a.csrf(a.transferAuditLinkAll)))
	mux.HandleFunc("POST /transfers/audit/unlink", a.requireAuth(a.csrf(a.transferAuditUnlink)))
	mux.HandleFunc("POST /transfers/audit/repair", a.requireAuth(a.csrf(a.transferAuditRePair)))
	mux.HandleFunc("POST /transfers/audit/counterparts", a.requireAuth(a.csrf(a.transferAuditCounterparts)))
	mux.HandleFunc("POST /transfers/audit/not-transfer", a.requireAuth(a.csrf(a.transferAuditNotTransfer)))
	mux.HandleFunc("POST /transfers/audit/loan", a.requireAuth(a.csrf(a.transferAuditLoan)))
	mux.HandleFunc("POST /transfers/audit/loan-all", a.requireAuth(a.csrf(a.transferAuditLoanAll)))
	mux.HandleFunc("POST /transfers/audit/normalize", a.requireAuth(a.csrf(a.transferAuditNormalize)))

	mux.HandleFunc("POST /undo", a.requireAuth(a.csrf(a.undo)))

	mux.HandleFunc("GET /more", a.requireAuth(a.more))
	mux.HandleFunc("GET /settings", a.requireAuth(a.settingsPage))
	mux.HandleFunc("POST /settings", a.requireAuth(a.csrf(a.settingsPost)))
	mux.HandleFunc("GET /settings/backup", a.requireAuth(a.settingsBackup))
	mux.HandleFunc("GET /recycle", a.requireAuth(a.recyclePage))
	mux.HandleFunc("POST /recycle/{id}/restore", a.requireAuth(a.csrf(a.recycleRestore)))
	mux.HandleFunc("POST /coaching/snooze", a.requireAuth(a.csrf(a.coachingSnooze)))
	mux.HandleFunc("POST /coaching/dismiss", a.requireAuth(a.csrf(a.coachingDismiss)))
	mux.HandleFunc("POST /coaching/clear", a.requireAuth(a.csrf(a.coachingClear)))
	mux.HandleFunc("POST /transactions/draft", a.requireAuth(a.csrf(a.draftSave)))
	mux.HandleFunc("POST /transactions/draft/sync", a.requireAuth(a.csrf(a.draftSync)))
	mux.HandleFunc("GET /drafts", a.requireAuth(a.draftsPage))
	mux.HandleFunc("POST /drafts/{id}/promote", a.requireAuth(a.csrf(a.draftPromote)))
	mux.HandleFunc("POST /drafts/{id}/delete", a.requireAuth(a.csrf(a.draftDelete)))
	mux.HandleFunc("GET /categories", a.requireAuth(a.categoriesPage))
	mux.HandleFunc("POST /categories", a.requireAuth(a.csrf(a.categoryCreate)))
	mux.HandleFunc("POST /categories/split", a.requireAuth(a.csrf(a.categorySplit)))
	mux.HandleFunc("POST /categories/{id}", a.requireAuth(a.csrf(a.categoryUpdate)))
	mux.HandleFunc("POST /categories/{id}/move", a.requireAuth(a.csrf(a.categoryMove)))
	mux.HandleFunc("POST /categories/{id}/archive", a.requireAuth(a.csrf(a.categoryArchive)))
	mux.HandleFunc("POST /categories/{id}/restore", a.requireAuth(a.csrf(a.categoryRestore)))
	mux.HandleFunc("POST /categories/{id}/merge", a.requireAuth(a.csrf(a.categoryMerge)))
	mux.HandleFunc("GET /tags", a.requireAuth(a.tagsPage))
	mux.HandleFunc("POST /tags", a.requireAuth(a.csrf(a.tagCreate)))
	mux.HandleFunc("GET /tags/options", a.requireAuth(a.tagOptions))
	mux.HandleFunc("POST /tags/apply", a.requireAuth(a.csrf(a.tagApply)))
	mux.HandleFunc("POST /tags/{id}", a.requireAuth(a.csrf(a.tagRename)))
	mux.HandleFunc("POST /tags/{id}/merge", a.requireAuth(a.csrf(a.tagMerge)))
	mux.HandleFunc("POST /tags/{id}/archive", a.requireAuth(a.csrf(a.tagArchive)))
	mux.HandleFunc("POST /tags/{id}/restore", a.requireAuth(a.csrf(a.tagRestore)))
	mux.HandleFunc("POST /tags/{id}/delete", a.requireAuth(a.csrf(a.tagDelete)))
	mux.HandleFunc("GET /payees", a.requireAuth(a.payeesPage))
	mux.HandleFunc("POST /payees", a.requireAuth(a.csrf(a.payeeCreate)))

	mux.HandleFunc("GET /import", a.requireAuth(a.importHome))
	mux.HandleFunc("POST /import/upload", a.requireAuth(a.csrf(a.importUpload)))
	mux.HandleFunc("GET /import/{id}/map", a.requireAuth(a.importMapGet))
	mux.HandleFunc("POST /import/{id}/map", a.requireAuth(a.csrf(a.importMapPost)))
	mux.HandleFunc("GET /import/{id}/preview", a.requireAuth(a.importPreview))
	mux.HandleFunc("POST /import/{id}/rows/{row}", a.requireAuth(a.csrf(a.importRowAction)))
	mux.HandleFunc("POST /import/{id}/bulk", a.requireAuth(a.csrf(a.importBulk)))
	mux.HandleFunc("POST /import/{id}/commit", a.requireAuth(a.csrf(a.importCommit)))
	mux.HandleFunc("GET /import/{id}/done", a.requireAuth(a.importDone))
	mux.HandleFunc("POST /import/{id}/undo", a.requireAuth(a.csrf(a.importUndo)))
	mux.HandleFunc("POST /import/{id}/cancel", a.requireAuth(a.csrf(a.importCancel)))

	mux.HandleFunc("GET /categorize/options", a.requireAuth(a.categorizeOptions))
	mux.HandleFunc("GET /categorize/similar", a.requireAuth(a.categorizeSimilar))
	mux.HandleFunc("POST /categorize", a.requireAuth(a.csrf(a.categorizePost)))

	mux.HandleFunc("GET /review", a.requireAuth(a.reviewList))
	mux.HandleFunc("POST /review/{id}/confirm", a.requireAuth(a.csrf(a.reviewConfirm)))
	mux.HandleFunc("POST /review/confirm-bulk", a.requireAuth(a.csrf(a.reviewConfirmBulk)))

	mux.HandleFunc("GET /rules", a.requireAuth(a.rulesPage))
	mux.HandleFunc("POST /rules", a.requireAuth(a.csrf(a.ruleCreate)))
	mux.HandleFunc("POST /rules/{id}", a.requireAuth(a.csrf(a.ruleUpdate)))
	mux.HandleFunc("POST /rules/{id}/category", a.requireAuth(a.csrf(a.ruleCategory)))
	mux.HandleFunc("POST /rules/{id}/enabled", a.requireAuth(a.csrf(a.ruleEnabled)))
	mux.HandleFunc("POST /rules/{id}/apply", a.requireAuth(a.csrf(a.ruleApply)))
	mux.HandleFunc("POST /rules/{id}/delete", a.requireAuth(a.csrf(a.ruleDelete)))
	mux.HandleFunc("POST /rules/{id}/move", a.requireAuth(a.csrf(a.ruleMove)))

	mux.HandleFunc("GET /profiles", a.requireAuth(a.profilesPage))
	mux.HandleFunc("POST /profiles/{id}/delete", a.requireAuth(a.csrf(a.profileDelete)))

	mux.HandleFunc("GET /transactions/export.csv", a.requireAuth(a.exportCSV))

	return mux
}

type authedHandler func(http.ResponseWriter, *http.Request, *auth.Session)

func (a *App) requireAuth(next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		needs, err := needsSetup(a.DB)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if needs {
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}
		sess, err := auth.SessionFromRequest(r, a.DB)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r, sess)
	}
}

func (a *App) csrf(next authedHandler) authedHandler {
	return func(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
		token := r.Header.Get("X-CSRF-Token")
		if token == "" {
			ct := r.Header.Get("Content-Type")
			if strings.HasPrefix(ct, "multipart/form-data") {
				// Parse multipart explicitly; avoid ParseForm which leaves Form
				// empty for multipart and prevents later FormValue from reading.
				_ = r.ParseMultipartForm(32 << 20)
				if r.MultipartForm != nil {
					if vals := r.MultipartForm.Value["csrf"]; len(vals) > 0 {
						token = vals[0]
					}
				}
			}
			if token == "" {
				token = r.FormValue("csrf")
			}
		}
		if token == "" || token != sess.CSRFToken {
			http.Error(w, "invalid CSRF token", http.StatusForbidden)
			return
		}
		next(w, r, sess)
	}
}

func needsSetup(db *sql.DB) (bool, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(1) FROM households`).Scan(&n)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return true, nil
		}
		return false, err
	}
	return n == 0, nil
}

func (a *App) manifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/manifest+json")
	_, _ = w.Write([]byte(`{
  "name": "Household Finance",
  "short_name": "Ledger",
  "start_url": "/",
  "display": "standalone",
  "background_color": "#f3f1ec",
  "theme_color": "#2f5d50",
  "icons": []
}`))
}

func wantsDatastar(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/event-stream") ||
		r.Header.Get("Datastar-Request") == "true" ||
		strings.Contains(strings.ToLower(r.Header.Get("Accept")), "datastar")
}

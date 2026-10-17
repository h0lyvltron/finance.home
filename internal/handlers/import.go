package handlers

import (
	"fmt"
	"io"
	"net/http"
	"strconv"

	"finance.home/internal/auth"
	"finance.home/internal/importcsv"
	"finance.home/internal/store"
)

func (a *App) importHome(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	d := a.base(r, sess)
	d.Nav = "more"
	a.withQuickAdd(&d)
	batches, _ := a.Store.ListImportBatches(20)
	d.Batches = batches
	profiles, _ := a.Store.ListMappingProfiles()
	d.Profiles = profiles
	a.Render.RenderHTTP(w, "import-home", d)
}

func (a *App) importUpload(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	if err := r.ParseMultipartForm(12 << 20); err != nil {
		http.Error(w, "upload too large or invalid", 400)
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		d := a.base(r, sess)
		d.Nav = "more"
		d.Error = "Choose a CSV file."
		batches, _ := a.Store.ListImportBatches(20)
		d.Batches = batches
		a.Render.RenderHTTP(w, "import-home", d)
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	id, err := a.Store.CreateImportBatch(store.CreateBatchInput{
		Filename:  hdr.Filename,
		RawCSV:    string(raw),
		CreatedBy: sess.PersonID,
	})
	if err != nil {
		d := a.base(r, sess)
		d.Error = err.Error()
		a.Render.RenderHTTP(w, "import-home", d)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/import/%d/map", id), http.StatusSeeOther)
}

func (a *App) importMapGet(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	b, err := a.Store.GetImportBatch(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	d := a.base(r, sess)
	d.Nav = "more"
	a.withQuickAdd(&d)
	d.Batch = b
	profiles, _ := a.Store.ListMappingProfiles()
	d.Profiles = profiles
	guess := importcsv.GuessColumns(b.Headers)
	d.Guess = guess
	d.Headers = b.Headers
	a.Render.RenderHTTP(w, "import-map", d)
}

func (a *App) importMapPost(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_ = r.ParseForm()
	acctID, _ := strconv.ParseInt(r.FormValue("account_id"), 10, 64)
	if acctID == 0 {
		d := a.base(r, sess)
		d.Error = "Account is required."
		b, _ := a.Store.GetImportBatch(id)
		d.Batch = b
		a.withQuickAdd(&d)
		a.Render.RenderHTTP(w, "import-map", d)
		return
	}
	profileID, _ := strconv.ParseInt(r.FormValue("profile_id"), 10, 64)
	in := store.MapBatchInput{
		BatchID:   id,
		AccountID: acctID,
		ProfileID: profileID,
		SaveAs:    r.FormValue("save_as"),
	}
	if profileID == 0 {
		invert := r.FormValue("invert_sign") == "1"
		hasHeader := r.FormValue("has_header") != "0"
		var acctPtr *int64
		acctPtr = &acctID
		in.Profile = store.MappingProfileInput{
			Name:         r.FormValue("save_as"),
			AccountID:    acctPtr,
			DateColumn:   r.FormValue("date_column"),
			DateFormat:   r.FormValue("date_format"),
			AmountMode:   r.FormValue("amount_mode"),
			AmountColumn: r.FormValue("amount_column"),
			DebitColumn:  r.FormValue("debit_column"),
			CreditColumn: r.FormValue("credit_column"),
			PayeeColumn:  r.FormValue("payee_column"),
			NotesColumn:  r.FormValue("notes_column"),
			InvertSign:   invert,
			HasHeader:    hasHeader,
			Delimiter:    r.FormValue("delimiter"),
		}
		if in.Profile.Delimiter == "" {
			in.Profile.Delimiter = ","
		}
		if in.Profile.Name == "" {
			in.Profile.Name = "CSV profile"
		}
	}
	if err := a.Store.MapAndStage(in); err != nil {
		d := a.base(r, sess)
		d.Error = err.Error()
		b, _ := a.Store.GetImportBatch(id)
		d.Batch = b
		a.withQuickAdd(&d)
		profiles, _ := a.Store.ListMappingProfiles()
		d.Profiles = profiles
		d.Headers = b.Headers
		d.Guess = importcsv.GuessColumns(b.Headers)
		a.Render.RenderHTTP(w, "import-map", d)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/import/%d/preview", id), http.StatusSeeOther)
}

func (a *App) importPreview(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	b, err := a.Store.GetImportBatch(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	excOnly := r.URL.Query().Get("exceptions") == "1"
	rows, total, err := a.Store.ListStagingRows(store.StagingFilter{
		BatchID:        id,
		Page:           page,
		PageSize:       25,
		ExceptionsOnly: excOnly,
	})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d := a.base(r, sess)
	d.Nav = "more"
	a.withQuickAdd(&d)
	d.Batch = b
	d.StagingRows = rows
	d.Page = page
	d.Total = total
	d.PageSize = 25
	d.ExceptionsOnly = excOnly
	pages := total / 25
	if total%25 != 0 {
		pages++
	}
	d.TotalPages = pages
	if drawer := r.URL.Query().Get("row"); drawer != "" {
		rid, _ := strconv.ParseInt(drawer, 10, 64)
		if row, err := a.Store.GetStagingRow(rid); err == nil {
			d.DrawerRow = row
		}
	}
	a.Render.RenderHTTP(w, "import-preview", d)
}

func (a *App) importRowAction(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	batchID, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	rowID, _ := strconv.ParseInt(r.PathValue("row"), 10, 64)
	_ = r.ParseForm()
	action := r.FormValue("action")
	var matchID *int64
	if v := r.FormValue("match_tx_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			matchID = &id
		}
	}
	if cat := r.FormValue("category_id"); cat != "" || r.FormValue("person") != "" || r.FormValue("payee") != "" {
		var catID *int64
		if cat != "" {
			id, _ := strconv.ParseInt(cat, 10, 64)
			if id > 0 {
				catID = &id
			}
		}
		_ = a.Store.UpdateStagingRow(rowID, catID, r.FormValue("person"), r.FormValue("payee"), r.FormValue("notes"), action)
	} else if action != "" {
		_ = a.Store.SetStagingAction(rowID, action, matchID)
	}
	redir := fmt.Sprintf("/import/%d/preview?page=%s", batchID, r.FormValue("page"))
	if r.FormValue("exceptions") == "1" {
		redir += "&exceptions=1"
	}
	http.Redirect(w, r, redir, http.StatusSeeOther)
}

func (a *App) importBulk(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	batchID, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_ = r.ParseForm()
	action := r.FormValue("bulk_action")
	var ids []int64
	for _, v := range r.Form["row_id"] {
		id, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			ids = append(ids, id)
		}
	}
	_ = a.Store.BulkStagingAction(batchID, ids, action)
	http.Redirect(w, r, fmt.Sprintf("/import/%d/preview", batchID), http.StatusSeeOther)
}

func (a *App) importCommit(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	batchID, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	n, err := a.Store.CommitImport(batchID, sess.PersonID)
	if err != nil {
		d := a.base(r, sess)
		d.Error = err.Error()
		b, _ := a.Store.GetImportBatch(batchID)
		d.Batch = b
		a.Render.RenderHTTP(w, "import-preview", d)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/import/%d/done?n=%d", batchID, n), http.StatusSeeOther)
}

func (a *App) importDone(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	batchID, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	b, err := a.Store.GetImportBatch(batchID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	d := a.base(r, sess)
	d.Nav = "more"
	d.Batch = b
	d.ImportedCount, _ = strconv.Atoi(r.URL.Query().Get("n"))
	a.withReviewBadge(&d)
	a.Render.RenderHTTP(w, "import-done", d)
}

func (a *App) importUndo(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	batchID, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := a.Store.UndoImportBatch(batchID, sess.PersonID); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/import?undone=1", http.StatusSeeOther)
}

func (a *App) exportCSV(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	acctID, _ := strconv.ParseInt(r.URL.Query().Get("account_id"), 10, 64)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="transactions.csv"`)
	f := store.RegisterFilter{AccountID: acctID, Limit: 10000}
	if err := a.Store.ExportTransactionsCSV(w, f); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tasks-remote/internal/cloudsync"
	"tasks-remote/internal/googleauth"
	"tasks-remote/internal/notify"
	"tasks-remote/internal/storage"
	"tasks-remote/internal/unlock"

	drive "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "tasks: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("tasks", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	dbPath := flags.String("db", defaultDBPath(), "path to local tasks database")
	if err := flags.Parse(args); err != nil {
		return err
	}
	rest := flags.Args()
	if len(rest) == 0 {
		usage()
		return nil
	}
	command := rest[0]
	commandArgs := rest[1:]

	switch command {
	case "init":
		secret, err := inputRecoverySecret()
		if err != nil {
			return err
		}
		if err := storage.Init(ctx, *dbPath, secret); err != nil {
			return err
		}
		if !secretFromEnv() {
			if err := unlock.Save(*dbPath, secret); err != nil {
				return err
			}
		}
		fmt.Printf("initialized %s\n", *dbPath)
		return nil
	case "unlock":
		secret, err := inputRecoverySecret()
		if err != nil {
			return err
		}
		store, err := storage.Open(ctx, *dbPath, secret)
		if err != nil {
			return err
		}
		if err := store.Close(); err != nil {
			return err
		}
		if !secretFromEnv() {
			if err := unlock.Save(*dbPath, secret); err != nil {
				return err
			}
		}
		fmt.Println("unlocked")
		return nil
	case "lock":
		if err := unlock.Clear(*dbPath); err != nil {
			return err
		}
		fmt.Println("locked")
		return nil
	case "add":
		addFlags := flag.NewFlagSet("add", flag.ContinueOnError)
		addFlags.SetOutput(os.Stderr)
		body := addFlags.String("body", "", "task body")
		dueRaw := addFlags.String("due", "", "due date/time as YYYY-MM-DD or RFC3339")
		remindRaw := addFlags.String("remind", "", "reminder date/time as YYYY-MM-DD or RFC3339")
		if err := addFlags.Parse(commandArgs); err != nil {
			return err
		}
		if len(addFlags.Args()) == 0 {
			return fmt.Errorf("add requires a title")
		}
		store, err := openStore(ctx, *dbPath)
		if err != nil {
			return err
		}
		defer store.Close()
		dueAt, err := parseOptionalTime(*dueRaw)
		if err != nil {
			return err
		}
		reminderAt, err := parseOptionalTime(*remindRaw)
		if err != nil {
			return err
		}
		task, err := store.AddTaskWithInput(ctx, storage.TaskInput{
			Title:      strings.Join(addFlags.Args(), " "),
			Body:       *body,
			DueAt:      dueAt,
			ReminderAt: reminderAt,
		})
		if err != nil {
			return err
		}
		fmt.Printf("%s %s\n", task.ID, task.Title)
		return nil
	case "list":
		store, err := openStore(ctx, *dbPath)
		if err != nil {
			return err
		}
		defer store.Close()
		tasks, err := store.ListTasks(ctx)
		if err != nil {
			return err
		}
		for _, task := range tasks {
			fmt.Printf("%s [%s] %s%s%s\n", task.ID, task.Status, task.Title, formatTags(task.Tags), formatDates(task))
		}
		return nil
	case "show":
		if len(commandArgs) != 1 {
			return fmt.Errorf("show requires a task id")
		}
		store, err := openStore(ctx, *dbPath)
		if err != nil {
			return err
		}
		defer store.Close()
		task, err := store.GetTask(ctx, commandArgs[0])
		if err != nil {
			return err
		}
		fmt.Printf("%s [%s]\n%s\n", task.ID, task.Status, task.Title)
		if len(task.Tags) > 0 {
			fmt.Printf("tags: %s\n", strings.Join(task.Tags, ", "))
		}
		if task.DueAt != nil {
			fmt.Printf("due: %s\n", task.DueAt.Format(time.RFC3339))
		}
		if task.ReminderAt != nil {
			fmt.Printf("reminder: %s\n", task.ReminderAt.Format(time.RFC3339))
		}
		if task.Body != "" {
			fmt.Printf("\n%s\n", task.Body)
		}
		return nil
	case "search":
		if len(commandArgs) == 0 {
			return fmt.Errorf("search requires a query")
		}
		store, err := openStore(ctx, *dbPath)
		if err != nil {
			return err
		}
		defer store.Close()
		tasks, err := store.SearchTasks(ctx, strings.Join(commandArgs, " "))
		if err != nil {
			return err
		}
		for _, task := range tasks {
			fmt.Printf("%s [%s] %s%s%s\n", task.ID, task.Status, task.Title, formatTags(task.Tags), formatDates(task))
		}
		return nil
	case "edit":
		editFlags := flag.NewFlagSet("edit", flag.ContinueOnError)
		editFlags.SetOutput(os.Stderr)
		body := editFlags.String("body", "", "task body")
		dueRaw := editFlags.String("due", "", "due date/time as YYYY-MM-DD or RFC3339")
		remindRaw := editFlags.String("remind", "", "reminder date/time as YYYY-MM-DD or RFC3339")
		if err := editFlags.Parse(commandArgs); err != nil {
			return err
		}
		if len(editFlags.Args()) < 2 {
			return fmt.Errorf("edit requires a task id and title")
		}
		store, err := openStore(ctx, *dbPath)
		if err != nil {
			return err
		}
		defer store.Close()
		dueAt, err := parseOptionalTime(*dueRaw)
		if err != nil {
			return err
		}
		reminderAt, err := parseOptionalTime(*remindRaw)
		if err != nil {
			return err
		}
		task, err := store.EditTaskWithInput(ctx, editFlags.Args()[0], storage.TaskInput{
			Title:      strings.Join(editFlags.Args()[1:], " "),
			Body:       *body,
			DueAt:      dueAt,
			ReminderAt: reminderAt,
		})
		if err != nil {
			return err
		}
		fmt.Printf("%s %s\n", task.ID, task.Title)
		return nil
	case "done":
		if len(commandArgs) != 1 {
			return fmt.Errorf("done requires a task id")
		}
		store, err := openStore(ctx, *dbPath)
		if err != nil {
			return err
		}
		defer store.Close()
		task, err := store.SetTaskStatus(ctx, commandArgs[0], "done")
		if err != nil {
			return err
		}
		fmt.Printf("%s [%s] %s\n", task.ID, task.Status, task.Title)
		return nil
	case "reopen":
		if len(commandArgs) != 1 {
			return fmt.Errorf("reopen requires a task id")
		}
		store, err := openStore(ctx, *dbPath)
		if err != nil {
			return err
		}
		defer store.Close()
		task, err := store.SetTaskStatus(ctx, commandArgs[0], "open")
		if err != nil {
			return err
		}
		fmt.Printf("%s [%s] %s\n", task.ID, task.Status, task.Title)
		return nil
	case "delete":
		if len(commandArgs) != 1 {
			return fmt.Errorf("delete requires a task id")
		}
		store, err := openStore(ctx, *dbPath)
		if err != nil {
			return err
		}
		defer store.Close()
		if err := store.DeleteTask(ctx, commandArgs[0]); err != nil {
			return err
		}
		fmt.Printf("deleted %s\n", commandArgs[0])
		return nil
	case "sync":
		return runSync(ctx, *dbPath, commandArgs)
	case "conflicts":
		return runConflicts(ctx, *dbPath, commandArgs)
	case "reminders":
		return runReminders(ctx, *dbPath, commandArgs)
	case "tag":
		return runTag(ctx, *dbPath, commandArgs)
	case "login":
		return runLogin(ctx, commandArgs)
	case "logout":
		return runLogout(commandArgs)
	case "export":
		return runExport(ctx, *dbPath, commandArgs)
	default:
		return fmt.Errorf("unknown command: %s", command)
	}
}

func runExport(ctx context.Context, dbPath string, args []string) error {
	exportFlags := flag.NewFlagSet("export", flag.ContinueOnError)
	exportFlags.SetOutput(os.Stderr)
	out := exportFlags.String("out", "", "plaintext export output path")
	confirm := exportFlags.Bool("confirm-plaintext", false, "confirm writing Sensitive Task Data in plaintext")
	if err := exportFlags.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return fmt.Errorf("export requires -out")
	}
	if !*confirm {
		return fmt.Errorf("export writes plaintext Sensitive Task Data; rerun with --confirm-plaintext")
	}
	store, err := openStore(ctx, dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	exportData, err := store.ExportPlaintext(ctx)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(exportData, "", "  ")
	if err != nil {
		return fmt.Errorf("encode plaintext export: %w", err)
	}
	file, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create plaintext export: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write plaintext export: %w", err)
	}
	if _, err := file.Write([]byte("\n")); err != nil {
		return fmt.Errorf("finish plaintext export: %w", err)
	}
	fmt.Printf("exported plaintext tasks to %s\n", *out)
	return nil
}

func runTag(ctx context.Context, dbPath string, args []string) error {
	if len(args) != 3 {
		return fmt.Errorf("tag requires: add <task-id> <tag> or remove <task-id> <tag>")
	}
	store, err := openStore(ctx, dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	switch args[0] {
	case "add":
		task, err := store.AddTag(ctx, args[1], args[2])
		if err != nil {
			return err
		}
		fmt.Printf("%s %s%s\n", task.ID, task.Title, formatTags(task.Tags))
		return nil
	case "remove":
		task, err := store.RemoveTag(ctx, args[1], args[2])
		if err != nil {
			return err
		}
		fmt.Printf("%s %s%s\n", task.ID, task.Title, formatTags(task.Tags))
		return nil
	default:
		return fmt.Errorf("unknown tag subcommand: %s", args[0])
	}
}

func runReminders(ctx context.Context, dbPath string, args []string) error {
	reminderFlags := flag.NewFlagSet("reminders", flag.ContinueOnError)
	reminderFlags.SetOutput(os.Stderr)
	within := reminderFlags.Duration("within", 24*time.Hour, "also show reminders coming due within this window")
	doNotify := reminderFlags.Bool("notify", false, "send a desktop notification for each due reminder")
	if err := reminderFlags.Parse(args); err != nil {
		return err
	}
	store, err := openStore(ctx, dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	reminders, err := store.Reminders(ctx, time.Now().UTC(), *within)
	if err != nil {
		return err
	}
	if len(reminders) == 0 {
		fmt.Println("no reminders due")
		return nil
	}
	for _, reminder := range reminders {
		label := "SOON"
		if reminder.Due {
			label = "DUE "
		}
		fmt.Printf("%s %s %s%s\n", label, reminder.Task.ID, reminder.Task.Title, formatReminderAt(reminder.Task))
	}
	if *doNotify {
		notifyDueReminders(reminders)
	}
	return nil
}

// notifyDueReminders fires a best-effort desktop notification per already-due
// reminder. Notification failures are reported but never fail the command, so
// a missing notifier cannot block reminder review. Reminders re-notify on each
// run until the task is completed; schedule the command at your preferred
// cadence (cron/systemd/launchd) to control how often you are reminded.
func notifyDueReminders(reminders []storage.DueReminder) {
	for _, reminder := range reminders {
		if !reminder.Due {
			continue
		}
		body := "Reminder due"
		if reminder.Task.ReminderAt != nil {
			body = "Reminder due " + reminder.Task.ReminderAt.Format("2006-01-02 15:04")
		}
		if err := notify.Send(reminder.Task.Title, body); err != nil {
			fmt.Fprintf(os.Stderr, "tasks: notification skipped: %v\n", err)
			return
		}
	}
}

func formatReminderAt(task storage.Task) string {
	if task.ReminderAt == nil {
		return ""
	}
	return " remind:" + task.ReminderAt.Format("2006-01-02")
}

func runConflicts(ctx context.Context, dbPath string, args []string) error {
	if len(args) > 0 && args[0] == "resolve" {
		return runConflictResolve(ctx, dbPath, args[1:])
	}
	if len(args) > 0 {
		return fmt.Errorf("unknown conflicts subcommand: %s", args[0])
	}
	store, err := openStore(ctx, dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	conflicts, err := store.ListConflictDetails(ctx)
	if err != nil {
		return err
	}
	if len(conflicts) == 0 {
		fmt.Println("no open conflicts")
		return nil
	}
	for _, conflict := range conflicts {
		fmt.Printf("%s %s task=%s\n", conflict.ID, conflict.Type, conflict.TaskID)
		fmt.Printf("  local:  %s\n", formatConflictSide(conflict.Local))
		fmt.Printf("  remote: %s\n", formatConflictSide(conflict.Remote))
		if conflict.Type == "duplicate_device_sequence" {
			fmt.Printf("  resolve: tasks-remote conflicts resolve %s\n", conflict.ID)
		} else {
			fmt.Printf("  resolve: tasks-remote conflicts resolve %s --use local|remote\n", conflict.ID)
		}
	}
	return nil
}

func runConflictResolve(ctx context.Context, dbPath string, args []string) error {
	// Pull the conflict id out first so it can appear before or after --use;
	// Go's flag package otherwise stops parsing at the first positional.
	conflictID, flagArgs := splitPositional(args)
	if conflictID == "" {
		return fmt.Errorf("conflicts resolve requires a conflict id")
	}
	resolveFlags := flag.NewFlagSet("conflicts resolve", flag.ContinueOnError)
	resolveFlags.SetOutput(os.Stderr)
	use := resolveFlags.String("use", "", "which side to keep: local or remote")
	if err := resolveFlags.Parse(flagArgs); err != nil {
		return err
	}
	if len(resolveFlags.Args()) != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(resolveFlags.Args(), " "))
	}
	store, err := openStore(ctx, dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.ResolveConflict(ctx, conflictID, *use); err != nil {
		return err
	}
	fmt.Printf("resolved %s\n", conflictID)
	return nil
}

// splitPositional returns the first non-flag argument and all remaining
// arguments, so a single positional can be mixed freely with flags.
func splitPositional(args []string) (string, []string) {
	positional := ""
	rest := make([]string, 0, len(args))
	for _, arg := range args {
		if positional == "" && !strings.HasPrefix(arg, "-") {
			positional = arg
			continue
		}
		rest = append(rest, arg)
	}
	return positional, rest
}

func formatConflictSide(side storage.ConflictSide) string {
	device := side.DeviceID
	if len(device) > 16 {
		device = device[:16]
	}
	if !side.Present {
		return fmt.Sprintf("[change %s] not stored (rejected duplicate)", side.ChangeID)
	}
	if side.Deleted {
		return fmt.Sprintf("[device %s] deleted task", device)
	}
	summary := side.Title
	if side.Body != "" {
		summary += " — " + side.Body
	}
	return fmt.Sprintf("[device %s] %s%s", device, summary, formatTags(side.Tags))
}

func runLogin(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "google" {
		return fmt.Errorf("only login google is implemented")
	}
	loginFlags := flag.NewFlagSet("login google", flag.ContinueOnError)
	loginFlags.SetOutput(os.Stderr)
	credentials := loginFlags.String("credentials", "", "Google OAuth desktop client credentials JSON")
	if err := loginFlags.Parse(args[1:]); err != nil {
		return err
	}
	config, err := googleauth.ConfigFromCredentialsFile(*credentials)
	if err != nil {
		return err
	}
	if err := googleauth.Login(ctx, config); err != nil {
		return err
	}
	fmt.Println("google login complete")
	return nil
}

func runLogout(args []string) error {
	if len(args) == 0 || args[0] != "google" {
		return fmt.Errorf("only logout google is implemented")
	}
	if err := googleauth.Logout(); err != nil {
		return err
	}
	fmt.Println("google logout complete")
	return nil
}

func runSync(ctx context.Context, dbPath string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("sync requires a subcommand")
	}
	switch args[0] {
	case "status":
		if len(args) != 1 {
			return fmt.Errorf("sync status does not accept arguments")
		}
		status, err := storage.ReadSyncStatus(ctx, dbPath)
		if err != nil {
			return err
		}
		if !status.Initialized {
			fmt.Println("sync: not initialized")
			return nil
		}
		fmt.Printf("sync: %d total changes, %d pending\n", status.TotalChanges, status.PendingChanges)
		if status.OpenConflicts > 0 {
			fmt.Printf("conflicts: %d open\n", status.OpenConflicts)
		}
		if status.LastChangeAt != nil {
			fmt.Printf("last change: %s\n", status.LastChangeAt.Format("2006-01-02T15:04:05Z07:00"))
		}
		return nil
	case "push":
		syncFlags := flag.NewFlagSet("sync push", flag.ContinueOnError)
		syncFlags.SetOutput(os.Stderr)
		dir := syncFlags.String("dir", "", "local sync directory")
		useGoogle := syncFlags.Bool("google", false, "use Google Drive app data folder")
		credentials := syncFlags.String("credentials", "", "Google OAuth desktop client credentials JSON")
		if err := syncFlags.Parse(args[1:]); err != nil {
			return err
		}
		client, err := syncClient(ctx, *dir, *useGoogle, *credentials)
		if err != nil {
			return err
		}
		store, err := openStore(ctx, dbPath)
		if err != nil {
			return err
		}
		defer store.Close()
		if err := cloudsync.Push(ctx, store, client); err != nil {
			return err
		}
		fmt.Println("pushed sync artifacts")
		return nil
	case "pull":
		syncFlags := flag.NewFlagSet("sync pull", flag.ContinueOnError)
		syncFlags.SetOutput(os.Stderr)
		dir := syncFlags.String("dir", "", "local sync directory")
		useGoogle := syncFlags.Bool("google", false, "use Google Drive app data folder")
		credentials := syncFlags.String("credentials", "", "Google OAuth desktop client credentials JSON")
		if err := syncFlags.Parse(args[1:]); err != nil {
			return err
		}
		client, err := syncClient(ctx, *dir, *useGoogle, *credentials)
		if err != nil {
			return err
		}
		store, err := openStore(ctx, dbPath)
		if err != nil {
			return err
		}
		defer store.Close()
		if err := cloudsync.Pull(ctx, store, client); err != nil {
			return err
		}
		fmt.Println("pulled sync artifacts")
		return nil
	case "restore":
		syncFlags := flag.NewFlagSet("sync restore", flag.ContinueOnError)
		syncFlags.SetOutput(os.Stderr)
		dir := syncFlags.String("dir", "", "local sync directory")
		useGoogle := syncFlags.Bool("google", false, "use Google Drive app data folder")
		credentials := syncFlags.String("credentials", "", "Google OAuth desktop client credentials JSON")
		if err := syncFlags.Parse(args[1:]); err != nil {
			return err
		}
		client, err := syncClient(ctx, *dir, *useGoogle, *credentials)
		if err != nil {
			return err
		}
		secret, err := inputRecoverySecret()
		if err != nil {
			return err
		}
		manifest, err := cloudsync.ReadManifest(ctx, client)
		if err != nil {
			return err
		}
		if err := storage.InitWithManifest(ctx, dbPath, secret, manifest); err != nil {
			return err
		}
		store, err := storage.Open(ctx, dbPath, secret)
		if err != nil {
			return err
		}
		if err := cloudsync.Pull(ctx, store, client); err != nil {
			store.Close()
			return err
		}
		if err := store.Close(); err != nil {
			return err
		}
		if !secretFromEnv() {
			if err := unlock.Save(dbPath, secret); err != nil {
				return err
			}
		}
		fmt.Println("restored sync artifacts")
		return nil
	default:
		return fmt.Errorf("unknown sync subcommand: %s", args[0])
	}
}

func syncClient(ctx context.Context, dir string, useGoogle bool, credentialsPath string) (cloudsync.Client, error) {
	if useGoogle {
		config, err := googleauth.ConfigFromCredentialsFile(credentialsPath)
		if err != nil {
			return nil, err
		}
		source, err := googleauth.TokenSource(ctx, config)
		if err != nil {
			return nil, err
		}
		service, err := drive.NewService(ctx, option.WithTokenSource(source))
		if err != nil {
			return nil, fmt.Errorf("create google drive service: %w", err)
		}
		return cloudsync.GoogleDriveClient{Service: service}, nil
	}
	if dir == "" {
		return nil, fmt.Errorf("sync requires -dir or -google")
	}
	return cloudsync.LocalDirClient{Dir: dir}, nil
}

func openStore(ctx context.Context, dbPath string) (*storage.Store, error) {
	secret, err := recoverySecret(dbPath)
	if err != nil {
		return nil, err
	}
	return storage.Open(ctx, dbPath, secret)
}

func recoverySecret(dbPath string) (string, error) {
	return unlock.Load(dbPath)
}

func inputRecoverySecret() (string, error) {
	if secret := os.Getenv(unlock.EnvSecret); secret != "" {
		return secret, nil
	}
	return unlock.Prompt("Recovery secret")
}

func secretFromEnv() bool {
	return os.Getenv(unlock.EnvSecret) != ""
}

func formatTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	formatted := make([]string, 0, len(tags))
	for _, tag := range tags {
		formatted = append(formatted, "#"+tag)
	}
	return " " + strings.Join(formatted, " ")
}

func formatDates(task storage.Task) string {
	var parts []string
	if task.DueAt != nil {
		parts = append(parts, "due:"+task.DueAt.Format("2006-01-02"))
	}
	if task.ReminderAt != nil {
		parts = append(parts, "remind:"+task.ReminderAt.Format("2006-01-02"))
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

func parseOptionalTime(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		value := parsed.UTC().Truncate(time.Second)
		return &value, nil
	}
	if parsed, err := time.ParseInLocation("2006-01-02", raw, time.Local); err == nil {
		value := parsed.UTC().Truncate(time.Second)
		return &value, nil
	}
	return nil, fmt.Errorf("invalid time %q: use YYYY-MM-DD or RFC3339", raw)
}

func defaultDBPath() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "tasks-remote", "tasks.db")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "tasks.db"
	}
	return filepath.Join(home, ".local", "share", "tasks-remote", "tasks.db")
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: tasks-remote [-db path] <command>

implemented:
  init
  unlock
  lock
  add [-body text] [-due date] [-remind date] <title>
  edit [-body text] [-due date] [-remind date] <task-id> <title>
  done <task-id>
  reopen <task-id>
  delete <task-id>
  tag add <task-id> <tag>
  tag remove <task-id> <tag>
  list
  show <task-id>
  search <query>
  conflicts
  conflicts resolve <conflict-id> --use local|remote
  reminders [-within dur] [-notify]
  sync status
  sync push -dir <path>
  sync pull -dir <path>
  sync restore -dir <path>
  login google -credentials <file>
  logout google
  export -out <path> --confirm-plaintext

unlock:
  run unlock once per database, or set TASKS_REMOTE_SECRET for automation`)
}

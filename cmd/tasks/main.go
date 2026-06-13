package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tasks-remote/internal/storage"
	"tasks-remote/internal/unlock"
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
		task, err := store.AddTask(ctx, strings.Join(addFlags.Args(), " "), *body)
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
			fmt.Printf("%s [%s] %s\n", task.ID, task.Status, task.Title)
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
			fmt.Printf("%s [%s] %s\n", task.ID, task.Status, task.Title)
		}
		return nil
	case "edit":
		editFlags := flag.NewFlagSet("edit", flag.ContinueOnError)
		editFlags.SetOutput(os.Stderr)
		body := editFlags.String("body", "", "task body")
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
		task, err := store.EditTask(ctx, editFlags.Args()[0], strings.Join(editFlags.Args()[1:], " "), *body)
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
		if len(commandArgs) != 1 || commandArgs[0] != "status" {
			return fmt.Errorf("only sync status is implemented")
		}
		status, err := storage.ReadSyncStatus(ctx, *dbPath)
		if err != nil {
			return err
		}
		if !status.Initialized {
			fmt.Println("sync: not initialized")
			return nil
		}
		fmt.Printf("sync: %d total changes, %d pending\n", status.TotalChanges, status.PendingChanges)
		if status.LastChangeAt != nil {
			fmt.Printf("last change: %s\n", status.LastChangeAt.Format("2006-01-02T15:04:05Z07:00"))
		}
		return nil
	case "login", "logout", "conflicts", "export", "tag":
		return fmt.Errorf("%s is planned but not implemented yet", command)
	default:
		return fmt.Errorf("unknown command: %s", command)
	}
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
	fmt.Fprintln(os.Stderr, `usage: tasks [-db path] <command>

implemented:
  init
  unlock
  lock
  add [-body text] <title>
  edit [-body text] <task-id> <title>
  done <task-id>
  reopen <task-id>
  delete <task-id>
  list
  show <task-id>
  search <query>
  sync status

unlock:
  run unlock once per database, or set TASKS_REMOTE_SECRET for automation`)
}

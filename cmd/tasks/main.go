package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tasks-remote/internal/storage"
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
		secret, err := recoverySecret()
		if err != nil {
			return err
		}
		if err := storage.Init(ctx, *dbPath, secret); err != nil {
			return err
		}
		fmt.Printf("initialized %s\n", *dbPath)
		return nil
	case "add":
		if len(commandArgs) == 0 {
			return fmt.Errorf("add requires a title")
		}
		store, err := openStore(ctx, *dbPath)
		if err != nil {
			return err
		}
		defer store.Close()
		task, err := store.AddTask(ctx, strings.Join(commandArgs, " "), "")
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
	case "lock", "unlock", "login", "logout", "sync", "conflicts", "export", "edit", "done", "reopen", "delete", "tag":
		return fmt.Errorf("%s is planned but not implemented yet", command)
	default:
		return fmt.Errorf("unknown command: %s", command)
	}
}

func openStore(ctx context.Context, dbPath string) (*storage.Store, error) {
	secret, err := recoverySecret()
	if err != nil {
		return nil, err
	}
	return storage.Open(ctx, dbPath, secret)
}

func recoverySecret() (string, error) {
	secret := os.Getenv("TASKS_REMOTE_SECRET")
	if secret == "" {
		return "", fmt.Errorf("TASKS_REMOTE_SECRET is required until keychain unlock is implemented")
	}
	return secret, nil
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
  add <title>
  list
  show <task-id>
  search <query>

temporary unlock:
  set TASKS_REMOTE_SECRET until keychain unlock is implemented`)
}

package main

import (
	"fmt"
	"os"

	"github.com/doganaydindev/dlqautopsy/internal/analyzer"
	"github.com/doganaydindev/dlqautopsy/internal/classifier"
	"github.com/doganaydindev/dlqautopsy/internal/collector"
	"github.com/doganaydindev/dlqautopsy/internal/normalizer"
	"github.com/doganaydindev/dlqautopsy/internal/reporter"
	"github.com/doganaydindev/dlqautopsy/internal/storage"
	"github.com/spf13/cobra"
)

var (
	dbPath    string
	queueURL  string
	format    string
	runName   string
	purge     bool
	awsRegion string
	className string
	signature string
	limit     int
	compareTo string
)

var rootCmd = &cobra.Command{
	Use:   "dlqautopsy",
	Short: "Dead Letter Autopsy Engine",
	Long:  `A deterministic analyzer for failed async jobs. Read dead-letter queues, classify failures, and generate actionable reports.`,
}

var inspectCmd = &cobra.Command{
	Use:   "inspect",
	Short: "Ingest messages from a DLQ into the local storage",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("Starting Collector for %s...\n", queueURL)
		fmt.Printf("Using database %s\n", dbPath)

		db, err := storage.NewDB(dbPath)
		if err != nil {
			return err
		}
		defer db.Close()

		col := collector.NewSQSCollector(db, queueURL, runName, collector.SQSOptions{
			Purge:  purge,
			Region: awsRegion,
		})

		err = col.Run(cmd.Context())
		if err != nil {
			return err
		}

		fmt.Println("Ingestion complete.")
		return nil
	},
}

var classifyCmd = &cobra.Command{
	Use:   "classify",
	Short: "Classify ingested failures deterministically",
	RunE: func(cmd *cobra.Command, args []string) error {
		db, err := storage.NewDB(dbPath)
		if err != nil {
			return err
		}
		defer db.Close()

		norm := normalizer.NewNormalizer(db, runName)
		if err := norm.Run(cmd.Context()); err != nil {
			return fmt.Errorf("normalizer failed: %w", err)
		}

		cls := classifier.NewClassifier(db, runName)
		if err := cls.Run(cmd.Context()); err != nil {
			return fmt.Errorf("classifier failed: %w", err)
		}

		// Print useful summary after classification
		return reporter.WriteClassifySummary(db, runName)
	},
}

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Generate an autopsy report",
	RunE: func(cmd *cobra.Command, args []string) error {
		db, err := storage.NewDB(dbPath)
		if err != nil {
			return err
		}
		defer db.Close()

		ana := analyzer.NewAnalyzer(db, runName)
		summary, err := ana.Run(cmd.Context())
		if err != nil {
			return fmt.Errorf("analyzer failed: %w", err)
		}

		switch format {
		case "json":
			return reporter.WriteJSON(summary)
		case "markdown", "md":
			return reporter.WriteMarkdown(summary)
		default:
			return reporter.WriteText(summary)
		}
	},
}

var detailCmd = &cobra.Command{
	Use:   "detail",
	Short: "Drill into individual messages for a failure class or signature",
	Long: `Show individual message details for a specific failure class or error signature.

Examples:
  dlqautopsy detail --name my-run --class "Poison Message"
  dlqautopsy detail --name my-run --class "Downstream Dependency" --limit 5
  dlqautopsy detail --name my-run --signature e3b0c442
  dlqautopsy detail --name my-run --class "Poison Message" --format json
  dlqautopsy detail --name my-run --class "Poison Message" --format markdown`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if className == "" && signature == "" {
			// List available classes
			db, err := storage.NewDB(dbPath)
			if err != nil {
				return err
			}
			defer db.Close()

			classes, err := db.ListFailureClasses(runName)
			if err != nil {
				return err
			}

			fmt.Printf("Available failure classes for run '%s':\n", runName)
			for _, c := range classes {
				fmt.Printf("  - %s\n", c)
			}
			fmt.Println("\nUse --class \"<name>\" or --signature \"<prefix>\" to drill in.")
			return nil
		}

		db, err := storage.NewDB(dbPath)
		if err != nil {
			return err
		}
		defer db.Close()

		var records []storage.DetailRecord
		var label string

		if signature != "" {
			records, err = db.GetDetailsBySignature(runName, signature, limit)
			label = fmt.Sprintf("Signature: %s*", signature)
		} else {
			records, err = db.GetDetailsByClass(runName, className, limit)
			label = className
		}
		if err != nil {
			return fmt.Errorf("failed to fetch details: %w", err)
		}

		if len(records) == 0 {
			fmt.Printf("No messages found for %s in run '%s'.\n", label, runName)
			return nil
		}

		switch format {
		case "json":
			return reporter.WriteDetailJSON(records)
		case "markdown", "md":
			return reporter.WriteDetailMarkdown(records, label)
		default:
			return reporter.WriteDetailText(records, label)
		}
	},
}

var diffCmd = &cobra.Command{
	Use:   "diff",
	Short: "Compare two inspection runs to detect new, resolved, or worsening failures",
	Long: `Compare failure classes between two runs to identify what changed.

Examples:
  dlqautopsy diff --name monday-check --compare tuesday-check
  dlqautopsy diff --name before-deploy --compare after-deploy --format json
  dlqautopsy diff --name before-deploy --compare after-deploy --format markdown`,
	RunE: func(cmd *cobra.Command, args []string) error {
		db, err := storage.NewDB(dbPath)
		if err != nil {
			return err
		}
		defer db.Close()

		baseClasses, err := db.GetClassSummaries(runName)
		if err != nil {
			return fmt.Errorf("failed to get base run classes: %w", err)
		}

		compareClasses, err := db.GetClassSummaries(compareTo)
		if err != nil {
			return fmt.Errorf("failed to get compare run classes: %w", err)
		}

		switch format {
		case "json":
			return reporter.WriteDiffJSON(runName, compareTo, baseClasses, compareClasses)
		case "markdown", "md":
			return reporter.WriteDiffMarkdown(runName, compareTo, baseClasses, compareClasses)
		default:
			return reporter.WriteDiffText(runName, compareTo, baseClasses, compareClasses)
		}
	},
}

var runsCmd = &cobra.Command{
	Use:   "runs",
	Short: "List all inspection runs stored in the database",
	RunE: func(cmd *cobra.Command, args []string) error {
		db, err := storage.NewDB(dbPath)
		if err != nil {
			return err
		}
		defer db.Close()

		runs, err := db.GetRuns()
		if err != nil {
			return fmt.Errorf("failed to list runs: %w", err)
		}

		return reporter.WriteRunsText(runs, dbPath)
	},
}

func init() {
	// Global flags
	rootCmd.PersistentFlags().StringVar(&dbPath, "db", "autopsy.db", "SQLite database path")

	// Inspect flags
	inspectCmd.Flags().StringVar(&queueURL, "queue-url", "", "The AWS SQS DLQ URL to ingest from")
	_ = inspectCmd.MarkFlagRequired("queue-url")
	inspectCmd.Flags().BoolVar(&purge, "purge", false, "Delete messages from DLQ after successful ingestion")
	inspectCmd.Flags().StringVar(&awsRegion, "region", "", "AWS region (overrides default config)")

	// Required runName filter for primary commands
	inspectCmd.Flags().StringVar(&runName, "name", "", "Name of the inspection run")
	_ = inspectCmd.MarkFlagRequired("name")

	classifyCmd.Flags().StringVar(&runName, "name", "", "Name of the inspection run")
	_ = classifyCmd.MarkFlagRequired("name")

	reportCmd.Flags().StringVar(&runName, "name", "", "Name of the inspection run")
	_ = reportCmd.MarkFlagRequired("name")

	// Report flags
	reportCmd.Flags().StringVar(&format, "format", "text", "Output format: text, json, markdown (or md)")

	// Detail flags
	detailCmd.Flags().StringVar(&runName, "name", "", "Name of the inspection run")
	_ = detailCmd.MarkFlagRequired("name")
	detailCmd.Flags().StringVar(&className, "class", "", "Failure class to inspect")
	detailCmd.Flags().StringVar(&signature, "signature", "", "Error signature prefix to inspect")
	detailCmd.Flags().IntVar(&limit, "limit", 20, "Maximum number of messages to show")
	detailCmd.Flags().StringVar(&format, "format", "text", "Output format: text, json, markdown (or md)")

	// Diff flags
	diffCmd.Flags().StringVar(&runName, "name", "", "Base run name")
	_ = diffCmd.MarkFlagRequired("name")
	diffCmd.Flags().StringVar(&compareTo, "compare", "", "Run name to compare against")
	_ = diffCmd.MarkFlagRequired("compare")
	diffCmd.Flags().StringVar(&format, "format", "text", "Output format: text, json, markdown (or md)")

	// Wire commands
	rootCmd.AddCommand(inspectCmd)
	rootCmd.AddCommand(classifyCmd)
	rootCmd.AddCommand(reportCmd)
	rootCmd.AddCommand(detailCmd)
	rootCmd.AddCommand(diffCmd)
	rootCmd.AddCommand(runsCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

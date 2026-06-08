package lhcmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yurika0211/luckyharness/internal/config"
	"github.com/yurika0211/luckyharness/internal/learning"
)

func addLearnCmd(root *cobra.Command) {
	learnCmd := &cobra.Command{
		Use:   "learn",
		Short: "项目课学习模式",
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "列出内置课程包",
		RunE:  runLearnList,
	}
	startCmd := &cobra.Command{
		Use:   "start <course>",
		Short: "开始或恢复一门课程",
		Args:  cobra.ExactArgs(1),
		RunE:  runLearnStart,
	}
	currentCmd := &cobra.Command{
		Use:   "current",
		Short: "查看当前学习模块",
		RunE:  runLearnCurrent,
	}
	labCmd := &cobra.Command{
		Use:   "lab",
		Short: "查看当前实验任务",
		RunE:  runLearnLab,
	}
	submitCmd := &cobra.Command{
		Use:   "submit <evidence>",
		Short: "提交当前实验的证据并推进进度",
		Args:  cobra.MinimumNArgs(1),
		RunE:  runLearnSubmit,
	}
	progressCmd := &cobra.Command{
		Use:   "progress",
		Short: "查看学习进度",
		RunE:  runLearnProgress,
	}

	learnCmd.AddCommand(listCmd, startCmd, currentCmd, labCmd, submitCmd, progressCmd)
	root.AddCommand(learnCmd)
}

func runLearnList(cmd *cobra.Command, args []string) error {
	for _, course := range learning.BuiltinCourses() {
		fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%d modules\n", course.ID, course.Title, len(course.Modules))
	}
	return nil
}

func runLearnStart(cmd *cobra.Command, args []string) error {
	course, ok := learning.FindCourse(args[0])
	if !ok {
		return fmt.Errorf("unknown course %q", args[0])
	}
	store, err := newLearningStore()
	if err != nil {
		return err
	}
	cp, err := store.StartCourse(course)
	if err != nil {
		return err
	}
	module, _ := course.ModuleByID(cp.CurrentModule)
	fmt.Fprintf(cmd.OutOrStdout(), "Started: %s\n", course.Title)
	fmt.Fprintf(cmd.OutOrStdout(), "Current: %s - %s\n", module.ID, module.Title)
	fmt.Fprintf(cmd.OutOrStdout(), "Progress: %s\n", store.Path())
	return nil
}

func runLearnCurrent(cmd *cobra.Command, args []string) error {
	course, cp, store, err := activeLearningState()
	if err != nil {
		return err
	}
	module, ok := course.ModuleByID(cp.CurrentModule)
	if !ok {
		return fmt.Errorf("current module %q not found in course %s", cp.CurrentModule, course.ID)
	}
	done, total := learning.CourseCompletion(course, cp)
	fmt.Fprintf(cmd.OutOrStdout(), "Course: %s\n", course.Title)
	fmt.Fprintf(cmd.OutOrStdout(), "Module: %s - %s\n", module.ID, module.Title)
	fmt.Fprintf(cmd.OutOrStdout(), "Objective: %s\n", module.Objective)
	fmt.Fprintf(cmd.OutOrStdout(), "Completion: %d/%d\n", done, total)
	fmt.Fprintf(cmd.OutOrStdout(), "Progress: %s\n", store.Path())
	return nil
}

func runLearnLab(cmd *cobra.Command, args []string) error {
	course, cp, _, err := activeLearningState()
	if err != nil {
		return err
	}
	module, ok := course.ModuleByID(cp.CurrentModule)
	if !ok {
		return fmt.Errorf("current module %q not found in course %s", cp.CurrentModule, course.ID)
	}
	printLab(cmd, module)
	return nil
}

func runLearnSubmit(cmd *cobra.Command, args []string) error {
	course, _, store, err := activeLearningState()
	if err != nil {
		return err
	}
	evidence := strings.TrimSpace(strings.Join(args, " "))
	cp, mp, err := store.SubmitEvidence(course, evidence, true)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Accepted evidence for %s (%d attempt%s).\n", mp.ModuleID, mp.Attempts, pluralS(mp.Attempts))
	if cp.CompletedAt != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Course completed: %s\n", course.Title)
		return nil
	}
	module, _ := course.ModuleByID(cp.CurrentModule)
	fmt.Fprintf(cmd.OutOrStdout(), "Next: %s - %s\n", module.ID, module.Title)
	return nil
}

func runLearnProgress(cmd *cobra.Command, args []string) error {
	course, cp, store, err := activeLearningState()
	if err != nil {
		return err
	}
	done, total := learning.CourseCompletion(course, cp)
	fmt.Fprintf(cmd.OutOrStdout(), "Course: %s\n", course.Title)
	fmt.Fprintf(cmd.OutOrStdout(), "Completion: %d/%d\n", done, total)
	for _, module := range course.Modules {
		mp := cp.Modules[module.ID]
		status := mp.Status
		if status == "" {
			status = "pending"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "- %s\t%s\tattempts=%d\n", module.ID, status, mp.Attempts)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Progress: %s\n", store.Path())
	return nil
}

func activeLearningState() (learning.Course, learning.CourseProgress, *learning.ProgressStore, error) {
	store, err := newLearningStore()
	if err != nil {
		return learning.Course{}, learning.CourseProgress{}, nil, err
	}
	progress, err := store.Load()
	if err != nil {
		return learning.Course{}, learning.CourseProgress{}, nil, err
	}
	if progress.ActiveCourseID == "" {
		return learning.Course{}, learning.CourseProgress{}, nil, fmt.Errorf("no active course; run `lh learn start lh-agent-systems`")
	}
	course, ok := learning.FindCourse(progress.ActiveCourseID)
	if !ok {
		return learning.Course{}, learning.CourseProgress{}, nil, fmt.Errorf("active course %q is not installed", progress.ActiveCourseID)
	}
	cp, ok := progress.Courses[progress.ActiveCourseID]
	if !ok {
		return learning.Course{}, learning.CourseProgress{}, nil, fmt.Errorf("active course %q has no progress", progress.ActiveCourseID)
	}
	return course, cp, store, nil
}

func newLearningStore() (*learning.ProgressStore, error) {
	mgr, err := config.NewManager()
	if err != nil {
		return nil, err
	}
	if err := mgr.Load(); err != nil {
		return nil, err
	}
	return learning.NewProgressStore(mgr.HomeDir()), nil
}

func printLab(cmd *cobra.Command, module learning.Module) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Module: %s - %s\n", module.ID, module.Title)
	fmt.Fprintf(out, "Lab: %s\n", module.Lab.ID)
	fmt.Fprintf(out, "Prompt: %s\n", module.Lab.Prompt)
	if len(module.Concepts) > 0 {
		fmt.Fprintf(out, "Concepts: %s\n", strings.Join(module.Concepts, ", "))
	}
	if len(module.Lab.AgentRoles) > 0 {
		fmt.Fprintf(out, "Agent roles: %s\n", strings.Join(module.Lab.AgentRoles, ", "))
	}
	if len(module.Lab.Commands) > 0 {
		fmt.Fprintln(out, "Commands:")
		for _, c := range module.Lab.Commands {
			fmt.Fprintf(out, "  - %s\n", c)
		}
	}
	if len(module.Lab.Evidence) > 0 {
		fmt.Fprintln(out, "Evidence:")
		for _, e := range module.Lab.Evidence {
			fmt.Fprintf(out, "  - %s\n", e)
		}
	}
	if len(module.Rubric) > 0 {
		fmt.Fprintln(out, "Rubric:")
		for _, r := range module.Rubric {
			fmt.Fprintf(out, "  - %s\n", r)
		}
	}
	fmt.Fprintf(out, "Deliverable: %s\n", module.Lab.Deliverable)
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

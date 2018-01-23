// package main
// But, this module should be in a separated package in the future.
package main

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/otiai10/daap"
	"github.com/otiai10/dkmachine/v0/dkmachine"
	"github.com/urfave/cli"
)

// Handler ...
type Handler struct {
	Image   string
	Script  string
	Verbose bool
	ctx     *cli.Context
}

// NewHandler ...
func NewHandler(ctx *cli.Context) (*Handler, error) {
	h := &Handler{ctx: ctx}

	h.Image = ctx.String("image")
	if h.Image == "" {
		return nil, fmt.Errorf("`--image` is required but not specified")
	}

	script := ctx.String("script")
	if script == "" {
		return nil, fmt.Errorf("`--script` is required (for now)")
	}
	script, err := filepath.Abs(script)
	if err != nil {
		return nil, err
	}
	h.Script = script

	h.Verbose = ctx.Bool("verbose")

	return h, nil
}

// HandleBunch ...
func (h *Handler) HandleBunch(tasks []*Task) <-chan *Job {

	results := make(chan *Job)
	done := make(chan bool)
	count := 0

	for _, task := range tasks {
		go func(t *Task) {
			results <- h.Handle(t)
			done <- true
		}(task)
	}

	go func() {
		defer close(done)
		defer close(results)
		for {
			<-done
			count++
			if count >= len(tasks) {
				return
			}
		}
	}()

	return results
}

// Handle ...
func (h *Handler) Handle(task *Task) *Job {

	job := &Job{Task: task}

	instance, err := h.generateMachineOption(task)
	if err != nil {
		return job.Errorf("failed to specify machine config: %v", err)
	}
	job.Instance = instance

	if h.Verbose {
		job.Logger = NewLogger(fmt.Sprintf("[%s]", job.Instance.Name), task.Index)
	}

	job.Logf("Creating docker machine")
	machine, err := dkmachine.Create(job.Instance)
	if err != nil {
		return job.Errorf("failed to create machine: %v", err)
	}
	job.Logf("The machine created successfully")

	defer func() {
		job.Logf("Deleting docker machine")
		machine.Remove()
		job.Logf("The machine deleted successfully")
	}()

	lifecycle := daap.NewContainer("awsub/lifecycle", daap.Args{
		Machine: &daap.MachineConfig{
			Host:     machine.Host(),
			CertPath: machine.CertPath(),
		},
		Mounts: []daap.Mount{
			daap.Volume("/tmp", "/tmp"),
		},
	})

	container := daap.NewContainer(h.Image, daap.Args{
		Machine: &daap.MachineConfig{
			Host:     machine.Host(),
			CertPath: machine.CertPath(),
		},
		Mounts: []daap.Mount{
			daap.Volume("/tmp", "/tmp"),
		},
	})

	ctx := context.Background()

	wg := new(sync.WaitGroup)
	wg.Add(2)
	go h.Warmup(ctx, lifecycle, job, wg)
	go h.Warmup(ctx, container, job, wg)
	wg.Wait()

	if job.Error != nil {
		return job
	}

	if err := h.Prepare(ctx, lifecycle, job); err != nil {
		return job.Errorf("failed to prepare input tasks: %v", err)
	}

	execution := &daap.Execution{
		Script: h.Script,
		Env:    task.ContainerEnv,
	}

	job.Logf("Sending command queue to the container")
	stream, err := container.Exec(ctx, execution)
	if err != nil {
		return job.Errorf("failed to exec script in the container: %v", err)
	}
	job.Logf("The command queued successfully")

	for payload := range stream {
		job.Logf("&%d> %s", payload.Type, string(payload.Data))
	}
	job.Logf("The command finished completely")

	if err := h.Finalize(ctx, lifecycle, job); err != nil {
		return job.Errorf("failed to finalize output of task: %v", err)
	}
	job.Logf("Successfully uploaded output files to your bucket")

	job.Logf("Everything completed. Good work :)")

	return job
}

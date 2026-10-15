package machine

import (
	"context"

	"github.com/takumin/seiro/internal/chapi"
	"github.com/takumin/seiro/internal/env"
	"github.com/takumin/seiro/internal/plan"
	"github.com/takumin/seiro/internal/qemu"
	"github.com/takumin/seiro/internal/spec"
)

// control operates the virtual machine monitor of a running machine through
// its control socket.
type control interface {
	// Running reports whether the guest is running.
	Running(ctx context.Context) (bool, error)
	// PowerButton asks the guest to power off.
	PowerButton(ctx context.Context) error
	// Shutdown stops the virtual machine monitor.
	Shutdown(ctx context.Context) error
}

// controlOf returns the control of the planned machine.
func controlOf(p *plan.Machine) control {
	if p.Spec != nil && p.Spec.VMMName() == spec.VMMQEMU {
		return qmpControl{qemu.NewQMP(p.APISocket)}
	}
	return chControl{chapi.New(p.APISocket)}
}

// Running returns how the supervisor of the planned machine asks whether the
// guest runs.
func Running(p *plan.Machine) func(ctx context.Context) (bool, error) {
	return controlOf(p).Running
}

// controlAt returns the control of the machine as it was last started.
func controlAt(paths *env.MachinePaths) control {
	var p plan.Machine
	if err := env.ReadJSON(paths.Plan(), &p); err != nil {
		// Without a plan, assume cloud-hypervisor, which seiro used to run alone.
		p = plan.Machine{}
	}
	// The socket is derived from the layout rather than trusted from the plan.
	p.APISocket = paths.APISocket()
	return controlOf(&p)
}

type chControl struct{ c *chapi.Client }

func (c chControl) Running(ctx context.Context) (bool, error) {
	info, err := c.c.Info(ctx)
	if err != nil {
		return false, err
	}
	return info.State == "Running", nil
}

func (c chControl) PowerButton(ctx context.Context) error { return c.c.PowerButton(ctx) }
func (c chControl) Shutdown(ctx context.Context) error    { return c.c.Shutdown(ctx) }

type qmpControl struct{ q *qemu.QMP }

func (c qmpControl) Running(ctx context.Context) (bool, error) {
	st, err := c.q.Status(ctx)
	if err != nil {
		return false, err
	}
	return st.Running, nil
}

func (c qmpControl) PowerButton(ctx context.Context) error { return c.q.PowerDown(ctx) }
func (c qmpControl) Shutdown(ctx context.Context) error    { return c.q.Quit(ctx) }

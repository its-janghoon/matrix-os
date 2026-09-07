package cli

import (
	"fmt"
	"io"
	"os"

	agentv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/agent/v1"
	"github.com/spf13/cobra"
)

// defaultAgentAddr is the node's default agent gRPC endpoint (node Agent.Addr).
// It is distinct from the market (9091) and inference (9092) endpoints so the
// agent subcommands reach the AgentService server.
const defaultAgentAddr = "127.0.0.1:9094"

// newAgentCommand builds `matrix agent` with deploy/list/get. It drives the
// node's matrix.agent.v1.AgentService: a client submits a WebAssembly module,
// the node instantiates and runs it, and the deployment is persisted so it
// survives a node restart. When the operator has configured a per-run price,
// each run is metered from the deploying account to the operator's account,
// settled through consensus.
//
// Because the agent server listens on its own address (default 127.0.0.1:9094),
// the agent subcommands accept a dedicated --agent-addr flag rather than the
// global --addr (which targets the market API on 9091).
func newAgentCommand(opts *globalOptions) *cobra.Command {
	var agentAddr string
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Deploy and inspect WebAssembly agents on a node",
		Long: `agent drives a node's matrix.agent.v1 AgentService.

Deploy a WebAssembly module to a running node: the node instantiates and runs
it, and persists the deployment so it survives a restart. When the operator has
configured a per-run price (agent.run_price), each run is metered from the
deploying account to the operator's account and settled through consensus.

The agent API listens on its own address (default 127.0.0.1:9094), set with
--agent-addr, distinct from the market API targeted by the global --addr.`,
		Args: cobra.NoArgs,
	}
	cmd.PersistentFlags().StringVar(&agentAddr, "agent-addr", defaultAgentAddr,
		"node agent gRPC endpoint host:port")
	cmd.AddCommand(
		newAgentDeployCommand(opts, &agentAddr),
		newAgentListCommand(opts, &agentAddr),
		newAgentGetCommand(opts, &agentAddr),
	)
	return cmd
}

func newAgentDeployCommand(opts *globalOptions, agentAddr *string) *cobra.Command {
	var (
		id       string
		wasmPath string
		deployer string
	)
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy and run a WebAssembly module on a node",
		Long: `deploy reads a .wasm file and submits it to the node, which persists the
deployment and runs the module once. The module's startup output and any run
error are printed. When the node meters agent runs, --deployer names the paying
account (its signing key must be resolvable by the node); an unaffordable or
keyless metered deploy is refused rather than run for free.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return fmt.Errorf("--id is required")
			}
			if wasmPath == "" {
				return fmt.Errorf("--wasm is required")
			}
			module, err := os.ReadFile(wasmPath)
			if err != nil {
				return fmt.Errorf("failed to read wasm module %s: %w", wasmPath, err)
			}
			if len(module) == 0 {
				return fmt.Errorf("wasm module %s is empty", wasmPath)
			}
			ac, err := dialAgent(*agentAddr, opts.APIKey)
			if err != nil {
				return err
			}
			defer ac.Close()
			ctx, cancel := callContext(cmd.Context(), opts)
			defer cancel()

			resp, err := ac.agent.DeployAgent(ctx, &agentv1.DeployAgentRequest{
				Id:         id,
				WasmModule: module,
				Deployer:   deployer,
			})
			if err != nil {
				return mapErr(*agentAddr, err)
			}
			return printAgent(cmd.OutOrStdout(), opts.JSON, resp.GetAgent(), resp.GetCharged())
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "agent ID (required)")
	cmd.Flags().StringVar(&wasmPath, "wasm", "", "path to the .wasm module to deploy (required)")
	cmd.Flags().StringVar(&deployer, "deployer", "", "account charged for the run when the node meters agent runs")
	return cmd
}

func newAgentListCommand(opts *globalOptions, agentAddr *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List deployed agents and their status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ac, err := dialAgent(*agentAddr, opts.APIKey)
			if err != nil {
				return err
			}
			defer ac.Close()
			ctx, cancel := callContext(cmd.Context(), opts)
			defer cancel()

			resp, err := ac.agent.ListAgents(ctx, &agentv1.ListAgentsRequest{})
			if err != nil {
				return mapErr(*agentAddr, err)
			}
			return printAgents(cmd.OutOrStdout(), opts.JSON, resp.GetAgents())
		},
	}
	return cmd
}

func newAgentGetCommand(opts *globalOptions, agentAddr *string) *cobra.Command {
	var id string
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Fetch a single deployed agent by ID",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if id == "" {
				return fmt.Errorf("--id is required")
			}
			ac, err := dialAgent(*agentAddr, opts.APIKey)
			if err != nil {
				return err
			}
			defer ac.Close()
			ctx, cancel := callContext(cmd.Context(), opts)
			defer cancel()

			resp, err := ac.agent.GetAgent(ctx, &agentv1.GetAgentRequest{Id: id})
			if err != nil {
				return mapErr(*agentAddr, err)
			}
			return printAgent(cmd.OutOrStdout(), opts.JSON, resp.GetAgent(), resp.GetAgent().GetLastCharge())
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "agent ID (required)")
	return cmd
}

// agentRow is the flattened, presentation-friendly view of a deployed agent for
// JSON and table output.
type agentRow struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	ModuleHash string `json:"module_hash"`
	ModuleSize uint64 `json:"module_size"`
	LastOutput string `json:"last_output"`
	LastError  string `json:"last_error"`
	LastCharge uint64 `json:"last_charge"`
}

// agentStatusString renders an agent status enum as a lowercase human string.
func agentStatusString(s agentv1.AgentStatus) string {
	switch s {
	case agentv1.AgentStatus_AGENT_STATUS_DEPLOYED:
		return "deployed"
	case agentv1.AgentStatus_AGENT_STATUS_RUNNING:
		return "running"
	case agentv1.AgentStatus_AGENT_STATUS_FAILED:
		return "failed"
	default:
		return "unspecified"
	}
}

func rowFromAgent(a *agentv1.Agent) agentRow {
	return agentRow{
		ID:         a.GetId(),
		Status:     agentStatusString(a.GetStatus()),
		ModuleHash: a.GetModuleHash(),
		ModuleSize: a.GetModuleSize(),
		LastOutput: a.GetLastOutput(),
		LastError:  a.GetLastError(),
		LastCharge: a.GetLastCharge(),
	}
}

// printAgent renders a single deployed agent as JSON or key/value lines. charged
// is the charge settled for this operation (0 when metering is disabled).
func printAgent(w io.Writer, asJSON bool, a *agentv1.Agent, charged uint64) error {
	r := rowFromAgent(a)
	if asJSON {
		return printJSON(w, struct {
			agentRow
			Charged uint64 `json:"charged"`
		}{agentRow: r, Charged: charged})
	}
	fmt.Fprintf(w, "id:          %s\n", r.ID)
	fmt.Fprintf(w, "status:      %s\n", r.Status)
	fmt.Fprintf(w, "module hash: %s\n", r.ModuleHash)
	fmt.Fprintf(w, "module size: %d\n", r.ModuleSize)
	fmt.Fprintf(w, "charged:     %d\n", charged)
	if r.LastOutput != "" {
		fmt.Fprintf(w, "output:      %s\n", r.LastOutput)
	}
	if r.LastError != "" {
		fmt.Fprintf(w, "error:       %s\n", r.LastError)
	}
	return nil
}

// printAgents renders a list of deployed agents as JSON or a table.
func printAgents(w io.Writer, asJSON bool, agents []*agentv1.Agent) error {
	rows := make([]agentRow, 0, len(agents))
	for _, a := range agents {
		rows = append(rows, rowFromAgent(a))
	}
	if asJSON {
		return printJSON(w, rows)
	}
	if len(rows) == 0 {
		fmt.Fprintln(w, "no agents deployed")
		return nil
	}
	fmt.Fprintf(w, "%-24s %-12s %-16s %s\n", "ID", "STATUS", "SIZE", "MODULE HASH")
	for _, r := range rows {
		hash := r.ModuleHash
		if len(hash) > 16 {
			hash = hash[:16]
		}
		fmt.Fprintf(w, "%-24s %-12s %-16d %s\n", r.ID, r.Status, r.ModuleSize, hash)
	}
	return nil
}

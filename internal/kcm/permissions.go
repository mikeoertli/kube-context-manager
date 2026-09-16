package kcm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	authv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// ExitError distinguishes a denied action from an unsuccessful access check.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e.Err == nil {
		return ""
	}
	return e.Err.Error()
}
func (e *ExitError) Unwrap() error { return e.Err }

type accessOptions struct {
	name, file, namespace string
	timeout               time.Duration
}

func (o *accessOptions) flags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&o.file, "file", "", "Disambiguate by source kubeconfig path within this profile")
	cmd.Flags().StringVarP(&o.namespace, "namespace", "n", "", "Namespace (default: inspected context's namespace, or default)")
	cmd.Flags().DurationVar(&o.timeout, "request-timeout", 10*time.Second, "Timeout for the live check; must be positive")
}
func (o accessOptions) target(s *Settings) (Context, string, error) {
	if o.timeout <= 0 {
		return Context{}, "", fmt.Errorf("--request-timeout must be positive")
	}
	cs, err := s.contexts(currentProfile())
	if err != nil {
		return Context{}, "", err
	}
	name, file := o.name, o.file
	if name == "" {
		name = os.Getenv("KCM_CONTEXT")
		if file == "" {
			file = os.Getenv("KCM_FILE")
		}
	}
	if name == "" {
		return Context{}, "", fmt.Errorf("select a context first or specify one to inspect")
	}
	if file != "" {
		file, err = expandPath(file)
		if err != nil {
			return Context{}, "", err
		}
	}
	c, err := selectContext(cs, name, file)
	if err != nil {
		return Context{}, "", err
	}
	ns := o.namespace
	if ns == "" {
		ns = c.Namespace
	}
	if ns == "" {
		ns = "default"
	}
	if problems := validation.IsDNS1123Label(ns); len(problems) > 0 {
		return Context{}, "", fmt.Errorf("invalid namespace %q: %s", ns, strings.Join(problems, "; "))
	}
	return c, ns, nil
}

// This client has no disk or discovery cache. Loading an explicit file and
// overriding CurrentContext avoids using either the source default or KUBECONFIG.
type accessClient struct {
	host   string
	client *http.Client
}

func newAccessClient(c Context, timeout time.Duration) (*accessClient, error) {
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: c.File},
		&clientcmd.ConfigOverrides{CurrentContext: c.Name}).ClientConfig()
	if err != nil {
		return nil, err
	}
	config.Timeout = timeout
	// Credential refresh must not rewrite the inspected source kubeconfig.
	config.AuthConfigPersister = nil
	client, err := rest.HTTPClientFor(config)
	if err != nil {
		return nil, err
	}
	// Never redirect a credential-bearing review to a different destination.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &accessClient{strings.TrimRight(config.Host, "/"), client}, nil
}
func (a *accessClient) request(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.host+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("live permission check failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var status metav1.Status
		_ = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&status)
		return fmt.Errorf("live permission check failed (HTTP %d): %s", response.StatusCode, status.Message)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(output); err != nil {
		return fmt.Errorf("invalid permission API response: %w", err)
	}
	return nil
}

func permissionsCommand(load func() (*Settings, error)) *cobra.Command {
	var o accessOptions
	var details bool
	cmd := &cobra.Command{Use: "permissions [context]", Short: "Inspect live permissions in one namespace; never cached",
		Long:    "Query the inspected context's SelfSubjectRulesReview on demand. Omit the context to use this shell's selection. Named contexts must be in the current profile; use --file for duplicate names. Does not switch contexts or modify kubeconfigs. The compact table preserves resource-name restrictions; --details prints the full returned status as JSON. Incomplete results are labeled: missing rules then mean unknown, not denied. No cache, automatic checks, or picker queries.",
		Example: "  kcm permissions\n  kcm permissions customer-a -n app\n  kcm permissions --details", Args: cobra.MaximumNArgs(1), ValidArgsFunction: contextCompletion(load),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				o.name = args[0]
			}
			s, err := load()
			if err != nil {
				return err
			}
			c, ns, err := o.target(s)
			if err != nil {
				return err
			}
			a, err := newAccessClient(c, o.timeout)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), o.timeout)
			defer cancel()
			request := authv1.SelfSubjectRulesReview{TypeMeta: metav1.TypeMeta{APIVersion: "authorization.k8s.io/v1", Kind: "SelfSubjectRulesReview"}, Spec: authv1.SelfSubjectRulesReviewSpec{Namespace: ns}}
			var response struct {
				Status *authv1.SubjectRulesReviewStatus `json:"status"`
			}
			if err = a.request(ctx, http.MethodPost, "/apis/authorization.k8s.io/v1/selfsubjectrulesreviews", request, &response); err != nil {
				return err
			}
			if response.Status == nil {
				return fmt.Errorf("permission API response has no status")
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Context: %s\nNamespace: %s\nConfig: %s\nChecked: %s (live; not cached)\n", c.Name, ns, c.File, time.Now().Format(time.RFC3339))
			status := response.Status
			if status.Incomplete || status.EvaluationError != "" {
				fmt.Fprintln(w, "Result: INCOMPLETE — missing rules are unknown, not denied.")
				if status.EvaluationError != "" {
					fmt.Fprintf(w, "Evaluation error: %s\n", status.EvaluationError)
				}
			} else {
				fmt.Fprintln(w, "Result: complete for this namespace at check time.")
			}
			if details {
				enc := json.NewEncoder(w)
				enc.SetIndent("", "  ")
				return enc.Encode(status)
			}
			return printPermissionRules(w, status)
		}}
	o.flags(cmd)
	cmd.Flags().BoolVar(&details, "details", false, "Print the full returned rule status as JSON after the scope header")
	return cmd
}

func sortedValues(values []string) string {
	seen := map[string]bool{}
	for _, v := range values {
		seen[v] = true
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}
func printPermissionRules(out io.Writer, status *authv1.SubjectRulesReviewStatus) error {
	// Only merge verbs when both the resource and its name restrictions match.
	type key struct{ resource, names string }
	rows := map[key][]string{}
	for _, rule := range status.ResourceRules {
		names := sortedValues(rule.ResourceNames)
		if names == "" {
			names = "any"
		}
		for _, group := range rule.APIGroups {
			for _, resource := range rule.Resources {
				if group != "" {
					resource += "." + group
				}
				k := key{resource, names}
				rows[k] = append(rows[k], rule.Verbs...)
			}
		}
	}
	keys := make([]key, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].resource == keys[j].resource {
			return keys[i].names < keys[j].names
		}
		return keys[i].resource < keys[j].resource
	})
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "\nRESOURCE\tVERBS\tRESOURCE NAMES")
	for _, k := range keys {
		fmt.Fprintf(w, "%s\t%s\t%s\n", k.resource, sortedValues(rows[k]), k.names)
	}
	if len(keys) == 0 {
		fmt.Fprintln(w, "(no resource rules returned)")
	}
	urls := map[string][]string{}
	for _, r := range status.NonResourceRules {
		for _, u := range r.NonResourceURLs {
			urls[u] = append(urls[u], r.Verbs...)
		}
	}
	if len(urls) > 0 {
		fmt.Fprintln(w, "\nNON-RESOURCE URL\tVERBS")
		paths := make([]string, 0, len(urls))
		for p := range urls {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			fmt.Fprintf(w, "%s\t%s\n", p, sortedValues(urls[p]))
		}
	}
	return w.Flush()
}

func canICommand(load func() (*Settings, error)) *cobra.Command {
	var o accessOptions
	var all bool
	var subresource string
	cmd := &cobra.Command{Use: "can-i <verb> <resource[.group][/name]>", Short: "Check a live API permission without executing the action",
		Long:    "Use SelfSubjectAccessReview to check one action with the inspected context's credentials. Resource names, singular names, and short names are resolved through live API discovery. Use --subresource for log, exec, or another subresource. Cluster-scoped resources are detected automatically; -A checks a namespaced action across all namespaces. Does not execute the action, switch context, or cache results. Prints yes or no: exit 0 means allowed, 1 means not allowed, 2 means the check failed or is inconclusive. An allowed result does not guarantee that admission policies or other execution requirements will permit the actual operation.",
		Example: "  kcm can-i list pods -n app\n  kcm can-i delete deployments.apps --context customer-a -n app\n  kcm can-i get pods/my-pod --subresource log -n app\n  kcm can-i list nodes\n  kcm can-i list pods -A",
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(2)(cmd, args); err != nil {
				return &ExitError{2, err}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			allowed, err := checkAccess(cmd, load, o, args[0], args[1], subresource, all)
			if err != nil {
				return &ExitError{2, err}
			}
			if allowed {
				fmt.Fprintln(cmd.OutOrStdout(), "yes")
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), "no")
			return &ExitError{Code: 1}
		}}
	o.flags(cmd)
	cmd.Flags().StringVar(&o.name, "context", "", "Inspect a context in this profile without switching")
	cmd.Flags().StringVar(&subresource, "subresource", "", "Check a subresource, such as log or exec")
	cmd.Flags().BoolVarP(&all, "all-namespaces", "A", false, "Check the action across all namespaces")
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return &ExitError{2, err} })
	_ = cmd.RegisterFlagCompletionFunc("context", contextCompletion(load))
	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
		var out []string
		if len(args) == 0 {
			for _, v := range []string{"get", "list", "watch", "create", "update", "patch", "delete", "deletecollection", "bind", "escalate", "impersonate"} {
				if strings.HasPrefix(v, prefix) {
					out = append(out, v)
				}
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
	return cmd
}

func checkAccess(cmd *cobra.Command, load func() (*Settings, error), o accessOptions, verb, target, subresource string, all bool) (bool, error) {
	if all && cmd.Flags().Changed("namespace") {
		return false, fmt.Errorf("use either --namespace or --all-namespaces")
	}
	if verb == "" || strings.ContainsAny(verb, " /\t\n") {
		return false, fmt.Errorf("specify one API verb, such as get, list, or delete")
	}
	resource, name, hasName := strings.Cut(target, "/")
	if resource == "" || (hasName && name == "") || strings.Contains(name, "/") {
		return false, fmt.Errorf("use resource[.group][/name]; use --subresource for log or exec")
	}
	if strings.ContainsAny(subresource, "/ \t\n") {
		return false, fmt.Errorf("specify one subresource")
	}
	s, err := load()
	if err != nil {
		return false, err
	}
	c, ns, err := o.target(s)
	if err != nil {
		return false, err
	}
	a, err := newAccessClient(c, o.timeout)
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), o.timeout)
	defer cancel()
	group, res, namespaced, err := a.resolveResource(ctx, resource)
	if err != nil {
		return false, err
	}
	if all || !namespaced {
		ns = ""
	}
	attrs := &authv1.ResourceAttributes{Namespace: ns, Verb: verb, Group: group, Resource: res, Subresource: subresource, Name: name}
	request := authv1.SelfSubjectAccessReview{TypeMeta: metav1.TypeMeta{APIVersion: "authorization.k8s.io/v1", Kind: "SelfSubjectAccessReview"}, Spec: authv1.SelfSubjectAccessReviewSpec{ResourceAttributes: attrs}}
	var response struct {
		Status *struct {
			authv1.SubjectAccessReviewStatus
			Allowed *bool `json:"allowed"`
		} `json:"status"`
	}
	if err = a.request(ctx, http.MethodPost, "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews", request, &response); err != nil {
		return false, err
	}
	status := response.Status
	if status == nil {
		return false, fmt.Errorf("permission API response has no status")
	}
	if status.Allowed == nil {
		return false, fmt.Errorf("permission API response has no allowed decision")
	}
	if *status.Allowed && status.Denied {
		return false, fmt.Errorf("permission API returned contradictory results")
	}
	if status.EvaluationError != "" {
		return false, fmt.Errorf("permission check inconclusive: %s", status.EvaluationError)
	}
	if status.Reason != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), status.Reason)
	}
	return *status.Allowed, nil
}

// Resolve the resource's actual API group and scope without kubectl's disk cache.
// Unqualified resources prefer the core group; ambiguous extension groups must
// be qualified so that a check never silently targets the wrong resource.
func (a *accessClient) resolveResource(ctx context.Context, target string) (string, string, bool, error) {
	resource, group, qualified := strings.Cut(target, ".")
	if resource == "*" {
		return "", "", false, fmt.Errorf("specify a concrete resource; use permissions to inspect wildcard rules")
	}
	matches := func(list metav1.APIResourceList) []metav1.APIResource {
		var out []metav1.APIResource
		for _, r := range list.APIResources {
			if strings.Contains(r.Name, "/") {
				continue
			}
			found := r.Name == resource || r.SingularName == resource
			for _, short := range r.ShortNames {
				found = found || short == resource
			}
			if found {
				out = append(out, r)
			}
		}
		return out
	}
	if !qualified || group == "" {
		var list metav1.APIResourceList
		if err := a.request(ctx, http.MethodGet, "/api/v1", nil, &list); err != nil {
			return "", "", false, err
		}
		if found := matches(list); len(found) == 1 {
			return "", found[0].Name, found[0].Namespaced, nil
		}
		if qualified {
			return "", "", false, fmt.Errorf("unknown core resource %q", resource)
		}
	}
	var groups []metav1.APIGroup
	if qualified {
		var g metav1.APIGroup
		if err := a.request(ctx, http.MethodGet, "/apis/"+url.PathEscape(group), nil, &g); err != nil {
			return "", "", false, err
		}
		groups = append(groups, g)
	} else {
		var list metav1.APIGroupList
		if err := a.request(ctx, http.MethodGet, "/apis", nil, &list); err != nil {
			return "", "", false, err
		}
		groups = list.Groups
	}
	type match struct {
		group    string
		resource metav1.APIResource
	}
	var found []match
	for _, g := range groups {
		if g.PreferredVersion.GroupVersion == "" {
			return "", "", false, fmt.Errorf("API group %q has no preferred version", g.Name)
		}
		var list metav1.APIResourceList
		if err := a.request(ctx, http.MethodGet, "/apis/"+g.PreferredVersion.GroupVersion, nil, &list); err != nil {
			return "", "", false, err
		}
		for _, r := range matches(list) {
			found = append(found, match{g.Name, r})
		}
	}
	if len(found) == 0 {
		return "", "", false, fmt.Errorf("unknown resource %q", target)
	}
	if len(found) > 1 {
		return "", "", false, fmt.Errorf("ambiguous resource %q; specify resource.group", target)
	}
	return found[0].group, found[0].resource.Name, found[0].resource.Namespaced, nil
}

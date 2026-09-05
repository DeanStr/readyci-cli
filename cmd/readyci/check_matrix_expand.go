package main

import (
	"strings"
)

func optionsForMatrixVariant(options remoteRunOptions, variant matrixVariant) remoteRunOptions {
	next := options
	next.LogicalChildID = defaultString(next.LogicalChildID, defaultString(next.CommandAlias, "check"))
	next.LogicalChildName = defaultString(next.LogicalChildName, next.CommandAlias)
	if variant.Name != "" {
		next.MatrixVariant = variant.Name
		next.LogicalChildID += ":" + variant.Name
		if next.CommandAlias != "" {
			next.CommandAlias += " [" + variant.Name + "]"
		}
	}
	if len(variant.Env) > 0 {
		next.Env = mergeStringMaps(next.Env, variant.Env)
		if next.IdempotencyKey != "" && variant.Name != "" {
			next.IdempotencyKey += ":" + variant.Name
		}
	}
	return next
}

func withDefaultRunGroup(options remoteRunOptions) remoteRunOptions {
	if options.RunGroupKey != "" {
		return options
	}
	groupKey := strings.TrimSpace(options.IdempotencyKey)
	if groupKey == "" {
		return options
	}
	options.RunGroupKey = groupKey
	options.RunGroupKind = defaultString(options.RunGroupKind, "check")
	options.RunGroupName = defaultString(options.RunGroupName, options.CommandAlias)
	options.LogicalChildID = defaultString(options.LogicalChildID, defaultString(options.CommandAlias, "check"))
	options.LogicalChildName = defaultString(options.LogicalChildName, options.CommandAlias)
	return options
}

func mergeStringMaps(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(override))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range override {
		out[key] = value
	}
	return out
}

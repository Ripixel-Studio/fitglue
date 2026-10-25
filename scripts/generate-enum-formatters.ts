#!/usr/bin/env npx ts-node
/**
 * generate-enum-formatters.ts
 *
 * Generates formatter functions for proto enums.
 * Output: TypeScript formatters for server and web, Go formatters for server.
 *
 * Usage: npx ts-node scripts/generate-enum-formatters.ts
 */

import * as fs from 'fs';
import * as path from 'path';
import { execSync } from 'child_process';

const GO_FORMATTERS_DIR = path.join(__dirname, '..', 'src', 'go', 'pkg', 'types', 'formatters');
const WEB_DIR = path.join(__dirname, '..', '..', 'web');
const WEB_PB_DIR = path.join(WEB_DIR, 'src', 'types', 'pb');
// Source of truth for enum parsing — use web repo TS proto files
const TS_PB_DIR = WEB_PB_DIR;

// Enums to generate formatters for, with their source file and display name overrides
interface EnumConfig {
  goPkg: string;
  goEnumName?: string;
  file: string;
  enumName: string;
  funcNameOverride?: string;
  prefix: string;
  displayNameOverrides?: Record<string, string>;
  defaultValue?: string;
  // Additional aliases for the parser: maps informal string → enum member name (without prefix)
  // e.g. { 'running': 'RUN', 'cycling': 'RIDE', 'bike': 'RIDE' }
  parseAliases?: Record<string, string>;
}

const ENUM_CONFIGS: EnumConfig[] = [
  {
    file: 'models/activity/source.ts',
    goPkg: 'pbactivity',
    enumName: 'ActivityType',
    prefix: 'ACTIVITY_TYPE_',
    displayNameOverrides: {
      'UNSPECIFIED': 'Workout',
      'EBIKE_RIDE': 'E-Bike Ride',
      'EMOUNTAIN_BIKE_RIDE': 'E-Mountain Bike Ride',
      'HIGH_INTENSITY_INTERVAL_TRAINING': 'HIIT',
    },
    defaultValue: 'Workout',
    parseAliases: {
      'running': 'RUN',
      'cycling': 'RIDE',
      'biking': 'RIDE',
      'bike': 'RIDE',
      'swimming': 'SWIM',
      'walking': 'WALK',
      'hiking': 'HIKE',
      'weights': 'WEIGHT_TRAINING',
      'weighttraining': 'WEIGHT_TRAINING',
      'strength': 'WEIGHT_TRAINING',
      'trailrun': 'TRAIL_RUN',
    },
  },
  {
    file: 'models/activity/standardized.ts',
    goPkg: 'pbactivity',
    enumName: 'MuscleGroup',
    prefix: 'MUSCLE_GROUP_',
    displayNameOverrides: {
      'UNSPECIFIED': 'Unknown',
    },
    defaultValue: 'Unknown',
  },
  {
    file: 'models/plugin/provider.ts',
    goPkg: 'pbplugin',
    enumName: 'DestinationType',
    funcNameOverride: 'Destination',
    goEnumName: 'DestinationType',
    prefix: 'DESTINATION_',
    displayNameOverrides: {
      'UNSPECIFIED': 'Unknown',
      'GITHUB': 'GitHub',
      'TRAININGPEAKS': 'TrainingPeaks',
      'GOOGLESHEETS': 'Google Sheets',
      'INTERVALS': 'Intervals.icu',
    },
    defaultValue: 'Unknown',
  },
  {
    file: 'models/events/pipeline.ts',
    goPkg: 'pbevents',
    enumName: 'CloudEventType',
    prefix: 'CLOUD_EVENT_TYPE_',
    defaultValue: 'Unknown',
  },
  {
    file: 'models/events/pipeline.ts',
    goPkg: 'pbevents',
    enumName: 'CloudEventSource',
    prefix: 'CLOUD_EVENT_SOURCE_',
    defaultValue: 'Unknown',
  },
  {
    file: 'models/activity/source.ts',
    goPkg: 'pbactivity',
    enumName: 'ActivitySource',
    prefix: 'SOURCE_',
    displayNameOverrides: {
      'UNKNOWN': 'Unknown',
      'FILE_UPLOAD': 'File Upload',
      'PARKRUN_RESULTS': 'Parkrun Results',
      'APPLE_HEALTH': 'Apple Health',
      'HEALTH_CONNECT': 'Health Connect',
    },
    defaultValue: 'Unknown',
  },
  {
    file: 'models/plugin/provider.ts',
    goPkg: 'pbplugin',
    enumName: 'EnricherProviderType',
    prefix: 'ENRICHER_PROVIDER_',
    displayNameOverrides: {
      'UNSPECIFIED': 'Unknown',
      'FITBIT_HEART_RATE': 'Fitbit Heart Rate',
    },
    defaultValue: 'Unknown',
  },
  {
    file: 'models/pipeline/execution.ts',
    goPkg: 'pbpipeline',
    enumName: 'ExecutionStatus',
    prefix: 'STATUS_',
    defaultValue: 'Unknown',
  },
  {
    file: 'models/plugin/manifest.ts',
    goPkg: 'pbplugin',
    enumName: 'ConfigFieldType',
    prefix: 'CONFIG_FIELD_TYPE_',
    defaultValue: 'String',
  },
  {
    file: 'models/plugin/manifest.ts',
    goPkg: 'pbplugin',
    enumName: 'IntegrationAuthType',
    prefix: 'INTEGRATION_AUTH_TYPE_',
    displayNameOverrides: {
      'UNSPECIFIED': 'Manual',
      'APP_SYNC': 'App Sync',
      'API_KEY': 'API Key',
      'PUBLIC_ID': 'Public ID',
    },
    defaultValue: 'Manual',
  },
  {
    file: 'models/plugin/manifest.ts',
    goPkg: 'pbplugin',
    enumName: 'PluginType',
    prefix: 'PLUGIN_TYPE_',
    defaultValue: 'Unknown',
  },
  {
    file: 'models/plugin/provider.ts',
    goPkg: 'pbplugin',
    enumName: 'MuscleHeatmapPreset',
    prefix: 'MUSCLE_HEATMAP_PRESET_',
    defaultValue: 'Standard',
  },
  {
    file: 'models/plugin/provider.ts',
    goPkg: 'pbplugin',
    enumName: 'MuscleHeatmapStyle',
    prefix: 'MUSCLE_HEATMAP_STYLE_',
    defaultValue: 'Emoji Bars',
  },
  {
    file: 'models/pipeline/pending_input.ts',
    goPkg: 'pbpipeline',
    enumName: 'ParkrunResultsState',
    prefix: 'PARKRUN_RESULTS_STATE_',
    defaultValue: 'Pending',
  },
  {
    file: 'models/plugin/provider.ts',
    goPkg: 'pbplugin',
    enumName: 'VirtualGPSRoute',
    prefix: 'VIRTUAL_GPS_ROUTE_',
    defaultValue: 'None',
  },
  {
    file: 'models/plugin/provider.ts',
    goPkg: 'pbplugin',
    enumName: 'WorkoutSummaryFormat',
    prefix: 'WORKOUT_SUMMARY_FORMAT_',
    defaultValue: 'Compact',
  },
  {
    file: 'models/pipeline/pending_input.ts',
    goPkg: 'pbpipeline',
    enumName: 'PendingInput_Status',
    prefix: 'STATUS_',
    defaultValue: 'Waiting',
  },
  {
    file: 'models/pipeline/execution.ts',
    goPkg: 'pbpipeline',
    enumName: 'PipelineRunStatus',
    prefix: 'PIPELINE_RUN_STATUS_',
    displayNameOverrides: {
      'UNSPECIFIED': 'Unknown',
      'RUNNING': 'In Progress',
      'SYNCED': 'Synced',
      'PARTIAL': 'Partial',
      'FAILED': 'Failed',
      'PENDING': 'Pending',
      'SKIPPED': 'Skipped',
      'ARCHIVED': 'Archived',
    },
    defaultValue: 'Unknown',
  },
  {
    file: 'models/pipeline/execution.ts',
    goPkg: 'pbpipeline',
    enumName: 'DestinationStatus',
    prefix: 'DESTINATION_STATUS_',
    displayNameOverrides: {
      'UNSPECIFIED': 'Unknown',
      'PENDING': 'Pending',
      'SUCCESS': 'Success',
      'FAILED': 'Failed',
      'SKIPPED': 'Skipped',
    },
    defaultValue: 'Unknown',
  },
];

// Parse enum values from generated TypeScript file
function parseEnumFromFile(filePath: string, enumName: string): Array<{ name: string; value: number }> {
  const content = fs.readFileSync(filePath, 'utf-8');
  const enumPattern = new RegExp(`export enum ${enumName}\\s*\\{([^}]+)\\}`, 's');
  const match = content.match(enumPattern);

  if (!match) {
    console.error(`Could not find enum ${enumName} in ${filePath}`);
    return [];
  }

  const entries: Array<{ name: string; value: number }> = [];
  const valuePattern = /(\w+)\s*=\s*(-?\d+)/g;
  let valueMatch;

  while ((valueMatch = valuePattern.exec(match[1])) !== null) {
    entries.push({
      name: valueMatch[1],
      value: parseInt(valueMatch[2], 10),
    });
  }

  return entries;
}

// Convert SCREAMING_SNAKE_CASE to Title Case
function toTitleCase(str: string): string {
  return str
    .split('_')
    .map(word => word.charAt(0).toUpperCase() + word.slice(1).toLowerCase())
    .join(' ');
}

// Generate TypeScript formatter function
function generateTsFormatter(config: EnumConfig, entries: Array<{ name: string; value: number }>): string {
  const { enumName, prefix, displayNameOverrides = {}, defaultValue = 'Unknown' } = config;
  const funcName = `format${config.funcNameOverride || enumName}`;

  const mappingEntries: string[] = [];
  const cases: string[] = [];

  entries.forEach(entry => {
    if (entry.name === 'UNRECOGNIZED') return;

    const nameWithoutPrefix = entry.name.replace(prefix, '');
    const displayName = displayNameOverrides[nameWithoutPrefix] || toTitleCase(nameWithoutPrefix);

    cases.push(`    case ${enumName}.${entry.name}: return '${displayName}';`);

    // Add multiple variations to the mapping for flexible lookup
    mappingEntries.push(`    '${entry.name}': '${displayName}',`);
    mappingEntries.push(`    '${nameWithoutPrefix}': '${displayName}',`);
    mappingEntries.push(`    '${displayName}': '${displayName}',`);
    // Also support numeric string as a key just in case
    mappingEntries.push(`    '${entry.value}': '${displayName}',`);
  });

  return `
const ${enumName}Names: Record<string, string> = {
${Array.from(new Set(mappingEntries)).join('\n')}
};

export function ${funcName}(value: ${enumName} | number | string | undefined | null): string {
  if (value === undefined || value === null) return '${defaultValue}';

  if (typeof value === 'string') {
    // 1. Check mapping for enum names, normalized names, or already formatted names
    if (${enumName}Names[value]) return ${enumName}Names[value];

    // 2. Handle numeric strings not found in mapping
    const parsed = parseInt(value, 10);
    if (!isNaN(parsed)) {
      value = parsed;
    } else {
      // 3. Last resort: internal humanizer
      return value.replace(/[_-]/g, ' ').replace(/([A-Z])/g, ' $1').replace(/\\s+/g, ' ').trim()
        .split(' ').map(w => w.charAt(0).toUpperCase() + w.slice(1).toLowerCase()).join(' ');
    }
  }

  switch (value) {
${cases.join('\n')}
    default: return '${defaultValue}';
  }
}
`;
}

// Generate Go formatter function
function generateGoFormatter(config: EnumConfig, entries: Array<{ name: string; value: number }>): string {
  const { enumName, prefix, displayNameOverrides = {}, defaultValue = 'Unknown', goEnumName } = config;
  const typeName = goEnumName || enumName;
  const funcName = `Format${(config.funcNameOverride || enumName).replace(/_/g, '')}`;  // Remove underscores for function name

  // Check if this is a nested enum (contains underscore, like PendingInput_Status)
  const isNestedEnum = enumName.includes('_');

  const cases = entries
    .filter(e => e.name !== 'UNRECOGNIZED')
    .map(entry => {
      const nameWithoutPrefix = entry.name.replace(prefix, '');
      const displayName = displayNameOverrides[nameWithoutPrefix] || toTitleCase(nameWithoutPrefix);

      let entryName = entry.name;
      if (entryName.endsWith('_UNKNOWN')) entryName = entryName.replace('_UNKNOWN', '_UNSPECIFIED');

      // For nested enums, the Go constant is like: PendingInput_STATUS_UNSPECIFIED
      // For top-level enums, it's like: ActivityType_ACTIVITY_TYPE_UNSPECIFIED
      const goConstant = isNestedEnum
        ? `${config.goPkg}.${typeName.split('_')[0]}_${entryName}`  // PendingInput_STATUS_UNSPECIFIED
        : `${config.goPkg}.${typeName}_${entryName}`;                // ActivityType_ACTIVITY_TYPE_UNSPECIFIED

      return `\tcase ${goConstant}:\n\t\treturn "${displayName}"`;
    })
    .join('\n');

  return `
func ${funcName}(value ${config.goPkg}.${typeName}) string {
\tswitch value {
${cases}
\tdefault:
\t\treturn "${defaultValue}"
\t}
}
`;
}

// Generate TypeScript parser function (string -> enum)
function generateTsParser(config: EnumConfig, entries: Array<{ name: string; value: number }>): string {
  const { enumName, prefix, displayNameOverrides = {}, parseAliases = {} } = config;
  const funcName = `parse${config.funcNameOverride || enumName}`;

  // Find the default enum entry (UNSPECIFIED or first entry)
  const defaultEntry = entries.find(e => e.name.includes('UNSPECIFIED')) || entries[0];
  const defaultEnumValue = `${enumName}.${defaultEntry?.name || entries[0].name}`;

  // Collect entries, deduplicating by key (first entry wins)
  const seenKeys = new Set<string>();
  const mappingEntries: string[] = [];

  const addEntry = (key: string, enumValue: string) => {
    if (!seenKeys.has(key)) {
      seenKeys.add(key);
      mappingEntries.push(`    '${key}': ${enumValue},`);
    }
  };

  entries.forEach(entry => {
    if (entry.name === 'UNRECOGNIZED') return;

    const nameWithoutPrefix = entry.name.replace(prefix, '');
    const displayName = displayNameOverrides[nameWithoutPrefix] || toTitleCase(nameWithoutPrefix);
    const enumValue = `${enumName}.${entry.name}`;

    // Map all variations (lowercased) to enum value
    addEntry(entry.name.toLowerCase(), enumValue);            // e.g. 'activity_type_run'
    addEntry(nameWithoutPrefix.toLowerCase(), enumValue);     // e.g. 'run'
    addEntry(displayName.toLowerCase(), enumValue);           // e.g. 'run'
    addEntry(String(entry.value), enumValue);                 // e.g. '27'
  });

  // Add aliases from config
  for (const [alias, targetMember] of Object.entries(parseAliases)) {
    const targetEntry = entries.find(e => e.name === `${prefix}${targetMember}`);
    if (targetEntry) {
      addEntry(alias.toLowerCase(), `${enumName}.${targetEntry.name}`);
    }
  }

  return `
const ${enumName}Values: Record<string, ${enumName}> = {
${mappingEntries.join('\n')}
};

export function ${funcName}(input: string | number | undefined | null): ${enumName} {
  if (input === undefined || input === null) return ${defaultEnumValue};
  const key = String(input).toLowerCase().trim();
  if (${enumName}Values[key] !== undefined) return ${enumName}Values[key];
  return ${defaultEnumValue};
}
`;
}

// Generate Go parser function (string -> enum)
function generateGoParser(config: EnumConfig, entries: Array<{ name: string; value: number }>): string {
  const { enumName, prefix, displayNameOverrides = {}, parseAliases = {}, goEnumName } = config;
  const typeName = goEnumName || enumName;
  const funcName = `Parse${(config.funcNameOverride || enumName).replace(/_/g, '')}`;  // Remove underscores for function name

  const isNestedEnum = enumName.includes('_');

  // Find default constant
  const defaultEntry = entries.find(e => e.name.includes('UNSPECIFIED')) || entries[0];
  let defaultEntryName = defaultEntry.name;
  if (defaultEntryName.endsWith('_UNKNOWN')) defaultEntryName = defaultEntryName.replace('_UNKNOWN', '_UNSPECIFIED');
  const defaultGoConstant = isNestedEnum
    ? `${config.goPkg}.${typeName.split('_')[0]}_${defaultEntryName}`
    : `${config.goPkg}.${typeName}_${defaultEntryName}`;

  // Collect all map entries, deduplicating keys (first entry wins)
  const seenKeys = new Set<string>();
  const allGoEntries: string[] = [];

  const addEntry = (key: string, goConstant: string) => {
    if (!seenKeys.has(key)) {
      seenKeys.add(key);
      allGoEntries.push(`\t\t"${key}": ${goConstant},`);
    }
  };

  entries
    .filter(e => e.name !== 'UNRECOGNIZED')
    .forEach(entry => {
      const nameWithoutPrefix = entry.name.replace(prefix, '');
      const displayName = displayNameOverrides[nameWithoutPrefix] || toTitleCase(nameWithoutPrefix);

      let entryName = entry.name;
      if (entryName.endsWith('_UNKNOWN')) entryName = entryName.replace('_UNKNOWN', '_UNSPECIFIED');
      const goConstant = isNestedEnum
        ? `${config.goPkg}.${typeName.split('_')[0]}_${entryName}`
        : `${config.goPkg}.${typeName}_${entryName}`;

      // Add multiple keys: full name, short name, display name (all lowercased)
      addEntry(entry.name.toLowerCase(), goConstant);
      addEntry(nameWithoutPrefix.toLowerCase(), goConstant);
      addEntry(displayName.toLowerCase(), goConstant);
    });

  // Add aliases from config
  for (const [alias, targetMember] of Object.entries(parseAliases)) {
    const targetEntry = entries.find(e => e.name === `${prefix}${targetMember}`);
    if (!targetEntry) continue;
    const goConstant = isNestedEnum
      ? `${config.goPkg}.${typeName.split('_')[0]}_${targetEntry.name}`
      : `${config.goPkg}.${typeName}_${targetEntry.name}`;
    addEntry(alias.toLowerCase(), goConstant);
  }

  const mapEntries = allGoEntries.join('\n');


  return `
func ${funcName}(input string) ${config.goPkg}.${typeName} {
\t// Try exact proto enum name first (fast path)
\tif v, ok := ${config.goPkg}.${typeName}_value[input]; ok {
\t\treturn ${config.goPkg}.${typeName}(v)
\t}

\t// Case-insensitive lookup via display names, short names, and aliases
\tlookup := map[string]${config.goPkg}.${typeName}{
${mapEntries}
\t}

\tnormalized := strings.ToLower(strings.TrimSpace(input))
\tif v, ok := lookup[normalized]; ok {
\t\treturn v
\t}
\treturn ${defaultGoConstant}
}
`;
}

// Main generator
function main(): void {
  console.log('🔧 Generating enum formatters...\n');

  let tsContent = `// Code generated by generate-enum-formatters.ts. DO NOT EDIT.
/* eslint-disable */

import { MuscleGroup } from './models/activity/standardized';
import { ActivityType, ActivitySource } from './models/activity/source';
import { CloudEventType, CloudEventSource } from './models/events/pipeline';
import { DestinationType, EnricherProviderType, MuscleHeatmapPreset, MuscleHeatmapStyle, VirtualGPSRoute, WorkoutSummaryFormat } from './models/plugin/provider';
import { ExecutionStatus, PipelineRunStatus, DestinationStatus } from './models/pipeline/execution';
import { ConfigFieldType, IntegrationAuthType, PluginType } from './models/plugin/manifest';
import { PendingInput_Status, ParkrunResultsState } from './models/pipeline/pending_input';
`;

  let goContent = `// Code generated by generate-enum-formatters.ts. DO NOT EDIT.
package formatters

import (
\t"strings"

\tpbactivity "github.com/fitglue/server/src/go/pkg/types/pb/models/activity"\n\tpbevents "github.com/fitglue/server/src/go/pkg/types/pb/models/events"\n\tpbpipeline "github.com/fitglue/server/src/go/pkg/types/pb/models/pipeline"\n\tpbplugin "github.com/fitglue/server/src/go/pkg/types/pb/models/plugin"
)
`;

  for (const config of ENUM_CONFIGS) {
    const filePath = path.join(TS_PB_DIR, config.file);
    if (!fs.existsSync(filePath)) {
      console.warn(`⚠️  Skipping ${config.enumName}: file not found (${filePath})`);
      continue;
    }

    const entries = parseEnumFromFile(filePath, config.enumName);
    if (entries.length === 0) {
      console.warn(`⚠️  No entries found for ${config.enumName}`);
      continue;
    }

    console.log(`✅ ${config.enumName}: ${entries.length} values`);

    tsContent += generateTsFormatter(config, entries);
    tsContent += generateTsParser(config, entries);
    goContent += generateGoFormatter(config, entries);
    goContent += generateGoParser(config, entries);
  }

  // Server-side TS output removed — server is Go-only now.

  // Write Go formatters
  if (!fs.existsSync(GO_FORMATTERS_DIR)) {
    fs.mkdirSync(GO_FORMATTERS_DIR, { recursive: true });
  }
  const goOutputPath = path.join(GO_FORMATTERS_DIR, 'formatters.go');
  fs.writeFileSync(goOutputPath, goContent.trim() + '\n');
  // Run gofmt to ensure generated Go code passes lint
  try {
    execSync(`gofmt -w ${goOutputPath}`, { stdio: 'inherit' });
  } catch {
    console.warn('⚠️  gofmt not found or failed, skipping formatting');
  }
  console.log(`📁 Go: ${goOutputPath}`);

  // Copy to web if exists
  if (fs.existsSync(WEB_DIR)) {
    if (!fs.existsSync(WEB_PB_DIR)) {
      fs.mkdirSync(WEB_PB_DIR, { recursive: true });
    }
    const webOutputPath = path.join(WEB_PB_DIR, 'enum-formatters.ts');
    fs.writeFileSync(webOutputPath, tsContent.trim() + '\n');
    console.log(`📁 TypeScript (web): ${webOutputPath}`);
  } else {
    console.log('⏭️  Web directory not found, skipping web formatters');
  }

  console.log('\n✨ Done!');
}

main();

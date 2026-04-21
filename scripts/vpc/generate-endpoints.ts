// scripts/vpc/generate-endpoints.ts
// Usage: npx ts-node scripts/vpc/generate-endpoints.ts
// Run manually when AWS adds new PrivateLink services.
// Output is pasted into enum-patches.ts — do not auto-run on build.
//
// Queries us-east-1 (broadest service coverage) for all Amazon-owned
// interface endpoints and prints the formatted enum values array to stdout.
// Requires AWS credentials in the environment (any region access works —
// DescribeVpcEndpointServices is a global-ish API).

import {
  EC2Client,
  DescribeVpcEndpointServicesCommand,
  type ServiceDetail,
} from '@aws-sdk/client-ec2';

const REGION = 'us-east-1';

// ── PascalCase conversion ──────────────────────────────────
//
// Handles the common AWS service naming patterns:
//   secretsmanager   → SecretsManager
//   ecr.api          → EcrApi
//   ssmmessages      → SsmMessages
//   elasticloadbalancing → ElasticLoadBalancing
//
// Strategy:
//   1. Split on dots and hyphens first
//   2. For each segment, apply known acronym expansions, then
//      split on known word boundaries via a lookup table before
//      falling back to capitalising the whole segment.

const KNOWN_WORDS: string[] = [
  'elastic',
  'load',
  'balancing',
  'secrets',
  'manager',
  'messages',
  'certificate',
  'config',
  'cloud',
  'watch',
  'trail',
  'formation',
  'front',
  'search',
  'translate',
  'transcribe',
  'textract',
  'rekognition',
  'polly',
  'comprehend',
  'forecast',
  'personalize',
  'data',
  'sync',
  'pipeline',
  'glue',
  'athena',
  'redshift',
  'connect',
  'gateway',
  'transfer',
  'backup',
  'storage',
  'import',
  'export',
  'migration',
  'service',
  'catalog',
  'network',
  'firewall',
  'inspector',
  'guardduty',
  'detector',
  'macie',
  'security',
  'hub',
  'access',
  'analyzer',
  'audit',
  'codebuild',
  'codecommit',
  'codedeploy',
  'codepipeline',
  'codeartifact',
  'appstream',
  'workspaces',
  'workspace',
  'directory',
  'kinesis',
  'firehose',
  'streams',
  'stream',
  'events',
  'scheduler',
  'stepfunctions',
  'state',
  'machine',
  'lambda',
  'batch',
  'fargate',
  'container',
  'registry',
  'api',
  'execute',
  'appsync',
  'route',
  'hosted',
  'zone',
  'health',
  'shield',
  'waf',
  'regional',
  'global',
  'accelerator',
];

// Greedy left-to-right segmentation using the known-words list.
function segmentWord(s: string): string[] {
  if (s.length === 0) return [];
  for (let len = Math.min(s.length, 20); len >= 2; len--) {
    const candidate = s.slice(0, len);
    if (KNOWN_WORDS.includes(candidate)) {
      return [candidate, ...segmentWord(s.slice(len))];
    }
  }
  return [s];
}

function toPascalCase(serviceName: string): string {
  // Split on delimiters first
  const topSegments = serviceName.split(/[.\-_]/);

  const parts: string[] = [];
  for (const seg of topSegments) {
    const subWords = segmentWord(seg.toLowerCase());
    for (const word of subWords) {
      parts.push(word.charAt(0).toUpperCase() + word.slice(1));
    }
  }

  return parts.join('');
}

// ── Main ───────────────────────────────────────────────────

async function main(): Promise<void> {
  const client = new EC2Client({ region: REGION });

  const allServices: ServiceDetail[] = [];
  let nextToken: string | undefined;

  do {
    const cmd = new DescribeVpcEndpointServicesCommand({
      Filters: [
        { Name: 'service-type', Values: ['Interface'] },
        { Name: 'owner', Values: ['amazon'] },
      ],
      ...(nextToken ? { NextToken: nextToken } : {}),
    });

    const resp = await client.send(cmd);
    allServices.push(...(resp.ServiceDetails ?? []));
    nextToken = resp.NextToken;
  } while (nextToken);

  const REGION_PREFIX = `com.amazonaws.${REGION}.`;

  const serviceNames = allServices
    .map((svc) => svc.ServiceName ?? '')
    .filter((name) => name.startsWith(REGION_PREFIX))
    .map((name) => name.slice(REGION_PREFIX.length))
    // Drop aws.* and api.aws variants (different namespace)
    .filter((name) => !name.startsWith('aws.') && !name.startsWith('api.aws'))
    .sort();

  // Build enum value entries
  const lines: string[] = serviceNames.map((name) => {
    const key = toPascalCase(name);
    return `  { value: "${name}", name: "${key}" },`;
  });

  const today = new Date().toISOString().slice(0, 10);

  console.log(
    `// Generated ${today} from us-east-1 DescribeVpcEndpointServices`
  );
  console.log(
    `// Paste into the AwsVpcEndpointService enum values in enum-patches.ts`
  );
  console.log(`const values = [`);
  console.log(lines.join('\n'));
  console.log(`];`);
  console.log();
  console.log(`// Total: ${serviceNames.length} services`);
}

main().catch((err) => {
  console.error('Error:', err);
  process.exit(1);
});

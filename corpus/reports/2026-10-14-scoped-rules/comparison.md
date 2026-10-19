# Corpus comparison

1101 identical template/region cases; 6.0s deadline; 2GiB memory soft target.

Input checksums, schema source, harness, lint runtime, and run settings match. Machine load can still affect timing and timeout outcomes.

| Result | Before | After |
| --- | ---: | ---: |
| both-accept | 537 | 537 |
| both-reject | 144 | 150 |
| cue-accepts-lint-lifecycle-only | 250 | 250 |
| cue-accepts-lint-rejects | 60 | 54 |
| cue-rejects-lint-accepts | 18 | 18 |
| cue-unsupported | 83 | 83 |
| normalization-error | 9 | 9 |

## Changed outcomes

| Template | Region | Before | After |
| --- | --- | --- | --- |
| [tests/examples_output/RDS_with_DBParameterGroup.template](https://github.com/cloudtools/troposphere/blob/8cd7a25e5d3c65aee7e3e12d7abf9465c4a0d3c8/tests/examples_output/RDS_with_DBParameterGroup.template) | us-east-1 | cue-accepts-lint-rejects | both-reject |
| [test/fixtures/templates/public/rds-cluster.yaml](https://github.com/aws-cloudformation/cfn-lint/blob/be66fb3e224b41c85065cb3002ad0b24e31db535/test/fixtures/templates/public/rds-cluster.yaml) | us-east-1 | cue-accepts-lint-rejects | both-reject |
| [tests/translator/output/connector_appsync_to_table.json](https://github.com/aws/serverless-application-model/blob/a130d8b4c43895b860d5b01b0fcc71bc465b91cf/tests/translator/output/connector_appsync_to_table.json) | us-east-1 | cue-accepts-lint-rejects | both-reject |
| [tests/examples_output/AuroraServerlessRDS_SecretsManager.template](https://github.com/cloudtools/troposphere/blob/8cd7a25e5d3c65aee7e3e12d7abf9465c4a0d3c8/tests/examples_output/AuroraServerlessRDS_SecretsManager.template) | us-east-1 | cue-accepts-lint-rejects | both-reject |
| [test/fixtures/templates/bad/resources/rds/instance_sizes.yaml](https://github.com/aws-cloudformation/cfn-lint/blob/be66fb3e224b41c85065cb3002ad0b24e31db535/test/fixtures/templates/bad/resources/rds/instance_sizes.yaml) | us-east-1 | cue-accepts-lint-rejects | both-reject |
| [tests/examples_output/RDS_VPC.template](https://github.com/cloudtools/troposphere/blob/8cd7a25e5d3c65aee7e3e12d7abf9465c4a0d3c8/tests/examples_output/RDS_VPC.template) | us-east-1 | cue-accepts-lint-rejects | both-reject |

Timeouts and unsupported inputs remain unresolved. Agreement with cfn-lint does not establish deployability. Exact fingerprints and all transitions are in comparison.json.

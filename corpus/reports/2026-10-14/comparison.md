# Corpus comparison

1101 identical template/region cases; 6.0s deadline; 2GiB memory soft target.

Input checksums, schema source, harness, lint runtime, and run settings match. Machine load can still affect timing and timeout outcomes.

| Result | Before | After |
| --- | ---: | ---: |
| both-accept | 537 | 537 |
| both-reject | 139 | 144 |
| cue-accepts-lint-lifecycle-only | 250 | 250 |
| cue-accepts-lint-rejects | 66 | 60 |
| cue-rejects-lint-accepts | 18 | 18 |
| cue-unsupported | 82 | 83 |
| normalization-error | 9 | 9 |

## Changed outcomes

| Template | Region | Before | After |
| --- | --- | --- | --- |
| [tests/examples_output/S3_Bucket.template](https://github.com/cloudtools/troposphere/blob/8cd7a25e5d3c65aee7e3e12d7abf9465c4a0d3c8/tests/examples_output/S3_Bucket.template) | us-east-1 | cue-accepts-lint-rejects | both-reject |
| [test/fixtures/templates/bad/resources/lambda/required_properties.yaml](https://github.com/aws-cloudformation/cfn-lint/blob/be66fb3e224b41c85065cb3002ad0b24e31db535/test/fixtures/templates/bad/resources/lambda/required_properties.yaml) | us-east-1 | cue-accepts-lint-rejects | both-reject |
| [tests/translator/output/unsupported_resources.json](https://github.com/aws/serverless-application-model/blob/a130d8b4c43895b860d5b01b0fcc71bc465b91cf/tests/translator/output/unsupported_resources.json) | us-east-1 | both-reject | cue-unsupported |
| [tests/examples_output/S3_Bucket_With_AccelerateConfiguration.template](https://github.com/cloudtools/troposphere/blob/8cd7a25e5d3c65aee7e3e12d7abf9465c4a0d3c8/tests/examples_output/S3_Bucket_With_AccelerateConfiguration.template) | us-east-1 | cue-accepts-lint-rejects | both-reject |
| [tests/examples_output/S3_Bucket_With_Versioning_And_Lifecycle_Rules.template](https://github.com/cloudtools/troposphere/blob/8cd7a25e5d3c65aee7e3e12d7abf9465c4a0d3c8/tests/examples_output/S3_Bucket_With_Versioning_And_Lifecycle_Rules.template) | us-east-1 | cue-accepts-lint-rejects | both-reject |
| [test/fixtures/templates/bad/route53.yaml](https://github.com/aws-cloudformation/cfn-lint/blob/be66fb3e224b41c85065cb3002ad0b24e31db535/test/fixtures/templates/bad/route53.yaml) | us-east-1 | cue-accepts-lint-rejects | both-reject |
| [tests/examples_output/S3_Website_Bucket_With_Retain_On_Delete.template](https://github.com/cloudtools/troposphere/blob/8cd7a25e5d3c65aee7e3e12d7abf9465c4a0d3c8/tests/examples_output/S3_Website_Bucket_With_Retain_On_Delete.template) | us-east-1 | cue-accepts-lint-rejects | both-reject |

Timeouts and unsupported inputs remain unresolved. Agreement with cfn-lint does not establish deployability. Exact fingerprints and all transitions are in comparison.json.

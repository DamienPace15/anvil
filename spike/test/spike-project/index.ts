import * as anvil from '@anvil/cloud';

const bucket = new anvil.aws.Bucket('spike-bucket', {
  dataClassification: 'non-sensitive',
});

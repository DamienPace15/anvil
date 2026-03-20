import * as anvil from '@anvil/aws';

const bucket = new anvil.aws.Bucket('spike-bucket', {
  dataClassification: 'non-sensitive',
});

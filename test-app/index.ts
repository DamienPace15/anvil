import * as anvil from '@anvil/aws';

/* const myBucket = new anvil.Bucket('coreData', {
  dataClassification: 'sensitive',
  lifecycle: 90,
  recast: {},
});
 */

const bucket = new anvil.aws.Bucket('hi', { dataClassification: '' });

const lambda = new anvil.aws.Lambda('aaa', { name: `the-name-of-the-lambda` });

const func = new anvil.azure.FunctionApp('aa', {
  runtime: 'python',
  location: 'ss',
  name: 'damien',
  resourceGroupName: 'fefe',
});

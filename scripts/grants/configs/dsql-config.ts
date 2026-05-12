import { GrantMapConfig } from '../types';

export const DSQL_GRANT_CONFIG: GrantMapConfig = {
  tsFile: 'aws/dsql.ts',
  pyFile: 'aws/dsql.py',
  className: 'DSQL',
  arnMapProperty: 'clusterArns',
  pyArnMapProperty: 'cluster_arns',
  grants: [
    {
      method: 'grantConnect',
      actions: ['dsql:DbConnect'],
    },
  ],
};

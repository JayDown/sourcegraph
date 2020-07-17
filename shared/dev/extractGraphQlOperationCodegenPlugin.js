// @ts-check

const { visit } = require('graphql');
const path = require('path');

const ROOT_FOLDER = path.join(__dirname, '../../')
/**
 *
 * @param {import('graphql').GraphQLSchema} schema
 * @param {import('@graphql-codegen/plugin-helpers').Types.DocumentFile[]} documents
 * @param {{interfaceNameForOperations?: string}} config
 */
const plugin = (schema, documents, config) => {
  const { interfaceNameForOperations = 'AllOperations' } = config;

  /** @type {{name: string, location?: string}[]} */
  const allOperations = [];

  for (const item of documents) {
    if (item.document) {
      visit(item.document, {
        enter: {
          OperationDefinition: node => {
            if (node.name && node.name.value) {
              allOperations.push({
                name: node.name.value,
                location: item.location && path.relative(ROOT_FOLDER, item.location),
              });
            }
          },
        },
      });
    }
  }

  const interfaceFields = allOperations.map(
    ({ name, location }) => `
/** ${location || 'location not found'}*/
${name}: (variables: ${name}Variables) => ${name}Result;
`
  );
  if (interfaceFields.length === 0) {
    console.warn('No operations found');
    // TODO maybe throw an error?
    return '';
  }
  return `export interface ${interfaceNameForOperations} {
${interfaceFields.join('\n')}
}`;
};

module.exports = { plugin };

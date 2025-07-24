import path from 'path';
import { fileURLToPath } from 'url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

export default (env, argv) => {
  const isProduction = argv.mode === 'production';
  
  return {
    target: 'node',
    entry: './bin/rmq-vertical-scaler',
    output: {
      path: path.resolve(__dirname, 'dist'),
      filename: 'rmq-vertical-scaler.js',
      clean: true,
      module: true,
    },
    experiments: {
      outputModule: true,
    },
    externals: {
      // Keep Node.js built-ins as externals
      'node:test': 'commonjs2 node:test',
      'node:assert': 'commonjs2 node:assert',
      '@kubernetes/client-node': 'commonjs2 @kubernetes/client-node',
      'axios': 'commonjs2 axios',
      'commander': 'commonjs2 commander'
    },
    optimization: {
      minimize: isProduction,
      mangleExports: false,
    },
    devtool: isProduction ? 'source-map' : 'eval-source-map',
    mode: argv.mode || 'production',
    resolve: {
      extensions: ['.js', '.mjs'],
    },
    module: {
      rules: [
        {
          test: /\.m?js$/,
          resolve: {
            fullySpecified: false
          }
        }
      ]
    },
    stats: {
      errorDetails: true
    }
  };
};
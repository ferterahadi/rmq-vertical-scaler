import path from 'path';
import { fileURLToPath } from 'url';
import nodeExternals from 'webpack-node-externals';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

export default (env, argv) => {
  const isProduction = argv.mode === 'production';
  
  return {
    target: 'node',
    entry: './scale.js',
    output: {
      path: path.resolve(__dirname, 'dist'),
      filename: 'scale.js',
      clean: true,
    },
    externals: [
      nodeExternals({
        // Bundle axios and @kubernetes/client-node for smaller image
        allowlist: ['axios', '@kubernetes/client-node']
      })
    ],
    optimization: {
      // Disable minification to preserve function names and readability
      minimize: false,
      // Keep function names for better debugging
      mangleExports: false,
    },
    // Generate source maps for proper line mapping
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
    // Enable source map support for better debugging
    stats: {
      errorDetails: true
    }
  };
}; 
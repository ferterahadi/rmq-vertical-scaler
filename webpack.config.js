const path = require('path');
const webpack = require('webpack');

module.exports = {
  target: 'node',
  mode: 'production',
  entry: './scale.js',
  output: {
    path: path.resolve(__dirname, 'dist'),
    filename: 'bundle.js'
  },
  externals: {
    // Don't bundle these - they need native bindings
    '@kubernetes/client-node': 'commonjs @kubernetes/client-node'
  },
  optimization: {
    minimize: true,
    nodeEnv: false // Don't replace process.env.NODE_ENV
  },
  plugins: [
    new webpack.BannerPlugin({
      banner: '#!/usr/bin/env node',
      raw: true
    })
  ],
  node: {
    __dirname: false,
    __filename: false
  }
};
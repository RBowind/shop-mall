/**
 * Babel config for the Taro CLI build (`compiler: webpack5`).
 */
module.exports = {
  presets: [
    [
      "taro",
      {
        framework: "react",
        ts: true,
        compiler: "webpack5",
      },
    ],
  ],
};
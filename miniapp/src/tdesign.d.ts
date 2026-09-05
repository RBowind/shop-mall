/**
 * TDesign miniprogram components are native custom components consumed via
 * `usingComponents` (see app.config.ts). They render as kebab-case tags in
 * JSX, which the global JSX.IntrinsicElements (Taro's JSX types) does not
 * declare, so tsc needs these loose declarations. Props keep the TDesign
 * native names (camelCase), handlers follow the Taro hybrid rule:
 * onXxx → bind:xxx.
 */

type TDesignComponentProps = Record<string, unknown>;

declare global {
  namespace JSX {
    interface IntrinsicElements {
      "t-button": TDesignComponentProps;
      "t-cell": TDesignComponentProps;
      "t-cell-group": TDesignComponentProps;
      "t-icon": TDesignComponentProps;
      "t-tag": TDesignComponentProps;
      "t-input": TDesignComponentProps;
      "t-textarea": TDesignComponentProps;
      "t-switch": TDesignComponentProps;
      "t-empty": TDesignComponentProps;
      "t-loading": TDesignComponentProps;
      "t-stepper": TDesignComponentProps;
      "t-skeleton": TDesignComponentProps;
    }
  }
}

export {};

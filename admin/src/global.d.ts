// Type augmentation for Umi 4.7.7: the published types omit the runtime-only
// hooks (`history`, `useModel`, `useAccess`) even though `@umijs/max` injects
// them into the global scope at runtime. Adding a module declaration here
// keeps the strict `tsc --noEmit` gate quiet without weakening skipLibCheck.

declare module '@umijs/max' {
  export interface History {
    push: (path: string, state?: unknown) => void;
    replace: (path: string, state?: unknown) => void;
    goBack: () => void;
    location: {
      pathname: string;
      query: Record<string, string>;
      search: string;
      hash: string;
      state: unknown;
    };
    listen: (listener: (location: History['location']) => void) => () => void;
  }
  export const history: History;
  export function useModel<T = unknown>(namespace: string): T;
  export function useAccess(): Record<string, boolean>;
  export function getInitialState<T = unknown>(): T | undefined;
  export function setInitialState<T = unknown>(state: T): void;
}

export {};
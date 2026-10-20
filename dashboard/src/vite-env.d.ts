/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** Base URL of the Kyurem management API. Defaults to `/api`. */
  readonly VITE_KYUREM_API_BASE?: string;
  /** Optional bearer token for the Kyurem management API. */
  readonly VITE_KYUREM_API_TOKEN?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}

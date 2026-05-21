export interface CapabilitiesResponse {
  write_semantics_allowed: string[];
  supports_split_scope: boolean;
  supports_scopes_conflict: boolean;
  protocols: string[];
  validation_supported_roles: string[];
}

export function buildCapabilitiesResponse(): CapabilitiesResponse {
  return {
    write_semantics_allowed: ["WRITE_SEMANTICS_SYNC"],
    supports_split_scope: true,
    supports_scopes_conflict: true,
    protocols: ["claim_producer"],
    validation_supported_roles: [],
  };
}

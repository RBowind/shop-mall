/**
 * Buyer address service. Address `version` participates in the checkout
 * fingerprint so an edited address does not replay into a 409.
 */

import { getTransport } from "../lib/transport.ts";
import type {
  Address,
  AddressCreateRequest,
  AddressListResponse,
  AddressResponse,
  EmptyResponse,
  AddressUpdateRequest,
} from "./types.ts";

export async function listAddresses(): Promise<Address[]> {
  const response = await getTransport()({
    method: "GET",
    path: "/api/v1/addresses",
  });
  const envelope = response.data as AddressListResponse;
  return envelope.data.list;
}

export async function createAddress(
  input: AddressCreateRequest,
): Promise<Address> {
  const response = await getTransport()({
    method: "POST",
    path: "/api/v1/addresses",
    body: input,
  });
  return (response.data as AddressResponse).data;
}

export async function updateAddress(
  addressId: string,
  input: AddressUpdateRequest,
): Promise<Address> {
  const response = await getTransport()({
    method: "PATCH",
    path: `/api/v1/addresses/${encodeURIComponent(addressId)}`,
    body: input,
  });
  return (response.data as AddressResponse).data;
}

export async function deleteAddress(addressId: string): Promise<void> {
  await getTransport()({
    method: "DELETE",
    path: `/api/v1/addresses/${encodeURIComponent(addressId)}`,
  });
}
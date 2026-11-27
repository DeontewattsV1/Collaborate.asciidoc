// HTTP-shaped errors for the identity and room API. Messages are generic on
// purpose: a denied caller learns nothing about other rooms or identities.
export class RoomApiError extends Error {
  constructor(status, message) {
    super(message);
    this.name = "RoomApiError";
    this.status = status;
  }
}

export const unauthorized = () => new RoomApiError(401, "unauthorized");
export const denied = () => new RoomApiError(403, "room access denied");
export const rejected = (message = "invalid request") => new RoomApiError(400, message);
export const unavailable = (message) => new RoomApiError(503, message);

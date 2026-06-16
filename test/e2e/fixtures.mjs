const ticketURL = "https://example.test/status";

export const admin = {
  displayName: "Paolo Admin",
  email: "admin@example.test",
  password: "correct horse battery"
};

export const customer = {
  displayName: "Customer One",
  email: "customer@example.test",
  password: "customer horse battery"
};

export const ticket = {
  title: `E2E support request ${Date.now()}`,
  description: `The dashboard does not load for the customer smoke test. See ${ticketURL}.`,
  url: ticketURL,
  reply: "Visible E2E staff reply with the next step."
};

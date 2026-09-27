ALTER TABLE rental_listings DROP CONSTRAINT IF EXISTS rental_listings_owner_user_id_fkey;
ALTER TABLE rental_listings ADD CONSTRAINT rental_listings_owner_user_id_fkey FOREIGN KEY(owner_user_id) REFERENCES zonenan_users(id) ON DELETE CASCADE;
ALTER TABLE rental_listings DROP CONSTRAINT IF EXISTS rental_listings_merchant_id_fkey;
ALTER TABLE rental_listings ADD CONSTRAINT rental_listings_merchant_id_fkey FOREIGN KEY(merchant_id) REFERENCES merchants(id) ON DELETE CASCADE;
